package transcription

import (
	"log/slog"
	"sync"

	"github.com/nullswan/nomi/internal/logger"
)

type TranscriptionServer struct {
	logger *slog.Logger

	transcriptionHandler   *TranscriptionHandler
	bufferManagerPrimary   *BufferManager
	bufferManagerSecondary *BufferManager
	textReconciler         *TextReconciler
	callback               func(string)
	wg                     sync.WaitGroup
}

// NewTranscriptionServer initializes the TranscriptionServer with configurable options.
func NewTranscriptionServer(
	bufferManagerPrimary *BufferManager,
	bufferManagerSecondary *BufferManager,
	handler *TranscriptionHandler,
	reconciler *TextReconciler,
	logger *logger.Logger,
	callback func(string),
) *TranscriptionServer {
	return &TranscriptionServer{
		bufferManagerPrimary:   bufferManagerPrimary,
		bufferManagerSecondary: bufferManagerSecondary,
		transcriptionHandler:   handler,
		textReconciler:         reconciler,
		logger: logger.With(
			"component",
			"transcription_server",
		),
		callback: callback,
	}
}

func (ts *TranscriptionServer) Start() {
	ts.wg.Add(2)
	go ts.processLoop(ts.bufferManagerPrimary, "primary")
	go ts.processLoop(ts.bufferManagerSecondary, "secondary")
}

// AddAudio adds incoming audio data to the buffer manager.
func (ts *TranscriptionServer) AddAudio(audio []byte) {
	ts.bufferManagerPrimary.AddAudio(audio)
	ts.bufferManagerSecondary.AddAudio(audio)
}

// processLoop continuously listens for buffer flush signals to initiate transcription.
func (ts *TranscriptionServer) processLoop(bm *BufferManager, caller string) {
	defer ts.wg.Done()
	for {
		audioChunk, ok := bm.GetAudio()
		if !ok {
			return
		}
		if len(audioChunk.Data) == 0 {
			continue
		}

		// For now, we call it in a goroutine to avoid blocking the main loop.
		// In the future, we may want to consider a more sophisticated approach.
		go func() {
			transcribed, err := ts.transcriptionHandler.Transcribe(
				audioChunk.Data,
				caller,
			)
			if err != nil {
				ts.logger.
					With("error", err).
					Error("Failed to transcribe audio")

				return
			}

			ts.textReconciler.AddSegment(
				audioChunk.StartDuration,
				audioChunk.EndDuration,
				transcribed,
			)
			ts.callback(ts.textReconciler.GetCombinedText())
		}()
	}
}

// Close gracefully shuts down the TranscriptionServer.
func (ts *TranscriptionServer) Close() {
	ts.bufferManagerPrimary.Close()
	ts.bufferManagerSecondary.Close()
	ts.wg.Wait()
}

// Reset clears the buffer managers and text reconciler.
func (ts *TranscriptionServer) Reset() {
	ts.bufferManagerPrimary.Reset()
	ts.bufferManagerSecondary.Reset()

	ts.textReconciler.Reset()

	// TODO(nullswan): Cancel the context of the transcription handler
}
