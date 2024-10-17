package transcription

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/nullswan/nomi/internal/audio"
	"github.com/nullswan/nomi/internal/logger"
	openai "github.com/sashabaranov/go-openai"
)

type TranscriptionHandlerMetrics struct {
	transcriptions int
	errors         int

	totalDuration time.Duration

	mu sync.Mutex
}

func (m *TranscriptionHandlerMetrics) AddTranscription(duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.transcriptions++
	m.totalDuration += duration
}

func (m *TranscriptionHandlerMetrics) AddError() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.errors += 1
}

func (m *TranscriptionHandlerMetrics) GetTranscriptions() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.transcriptions
}

func (m *TranscriptionHandlerMetrics) GetErrors() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.errors
}

func (m *TranscriptionHandlerMetrics) GetTotalDuration() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.totalDuration
}

type TranscriptionHandler struct {
	logger         *logger.Logger
	client         *openai.Client
	contextTimeout time.Duration
	enableFixing   bool
	enableDumping  bool

	sampleRate     int
	channels       int
	bytesPerSample int
	bitsPerSample  int

	metrics TranscriptionHandlerMetrics
}

func NewTranscriptionHandler(
	apiKey string,
	audioOpts *audio.AudioOptions,
	logger *logger.Logger,
) *TranscriptionHandler {
	return &TranscriptionHandler{
		logger:         logger.With("component", "transcription_handler"),
		client:         openai.NewClient(apiKey),
		contextTimeout: 30 * time.Second,
		sampleRate:     int(audioOpts.SampleRate),
		channels:       audioOpts.Channels,
		bytesPerSample: audioOpts.BytesPerSample,
		bitsPerSample:  audioOpts.BitsPerSample,
	}
}

func (th *TranscriptionHandler) SetContextTimeout(duration time.Duration) {
	th.contextTimeout = duration
}

func (th *TranscriptionHandler) SetEnableFixing(enabled bool) {
	th.enableFixing = enabled
}

func (th *TranscriptionHandler) SetEnableDumping(enabled bool) {
	th.enableDumping = enabled
}

func (th *TranscriptionHandler) Transcribe(
	pcmData []byte,
	caller string,
) (string, error) {
	// Add WAV header to PCM data
	wavData, err := AddWAVHeader(
		pcmData,
		th.sampleRate,
		th.channels,
		th.bitsPerSample,
	)
	if err != nil {
		return "", fmt.Errorf("failed to add WAV header: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), th.contextTimeout)
	defer cancel()

	req := openai.AudioRequest{
		Model:    openai.Whisper1,
		Reader:   bytes.NewReader(wavData),
		FilePath: "audio.wav", // This is meaningless for OpenAI as we turn the audio data into a reader
	}

	startedAt := time.Now()
	resp, err := th.client.CreateTranscription(ctx, req)
	if err != nil {
		th.metrics.AddError()
		return "", fmt.Errorf("transcription error: %w", err)
	}

	reqDuration := time.Since(startedAt)
	th.metrics.AddTranscription(reqDuration)

	th.logger.
		With("duration", reqDuration).
		With("caller", caller).
		With("transcription", resp.Text).
		Debug("Transcription completed")

	if th.enableDumping {
		filename := fmt.Sprintf(
			"audio_%s_%d.wav",
			caller,
			time.Now().Unix(),
		)
		err := os.WriteFile(filename, wavData, 0644)
		if err != nil {
			th.logger.
				With("error", err).
				Error("Failed to write audio data to file")
		}

		th.logger.
			With("filename", filename).
			Info("Audio data dumped to file")
	}

	return resp.Text, nil
}

// GetMetrics returns the transcription handler metrics.
func (th *TranscriptionHandler) GetMetrics() *TranscriptionHandlerMetrics {
	return &th.metrics
}
