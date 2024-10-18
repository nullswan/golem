package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gordonklaus/portaudio"
	"github.com/nullswan/nomi/internal/audio"
	"github.com/nullswan/nomi/internal/logger"
	"github.com/nullswan/nomi/internal/transcription"
)

func main() {
	// Initialize logger
	logger := logger.Init()

	oaiKey := os.Getenv("OPENAI_API_KEY")
	if oaiKey == "" {
		logger.Error("OPENAI_API_KEY is not set")
		return
	}

	// Initialize PortAudio
	if err := portaudio.Initialize(); err != nil {
		logger.Error("Failed to initialize PortAudio", "error", err)
		return
	}
	defer portaudio.Terminate()

	audioOpts := &audio.AudioOptions{}
	audioOpts, err := audio.ComputeAudioOptions(audioOpts)
	if err != nil {
		logger.
			With("error", err).
			Error("Failed to compute audio options")
		return
	}

	callback := func(text string, isProcessing bool) {
		fmt.Println("Transcribed Text:", text, isProcessing)
	}

	bufferManagerPrimary := transcription.NewBufferManager(audioOpts)
	bufferManagerPrimary.SetMinBufferDuration(500 * time.Millisecond)
	bufferManagerPrimary.SetOverlapDuration(100 * time.Millisecond)

	bufferManagerSecondary := transcription.NewBufferManager(audioOpts)
	bufferManagerSecondary.SetMinBufferDuration(2 * time.Second)
	bufferManagerSecondary.SetOverlapDuration(400 * time.Millisecond)

	textReconcilier := transcription.NewTextReconciler(logger)
	tsHandler := transcription.NewTranscriptionHandler(
		oaiKey,
		audioOpts,
		logger,
	)
	tsHandler.SetEnableDumping(true)
	tsHandler.SetEnableFixing(true)

	ts := transcription.NewTranscriptionServer(
		bufferManagerPrimary,
		bufferManagerSecondary,
		tsHandler,
		textReconcilier,
		logger,
		callback,
	)
	ts.Start()
	defer ts.Close()

	// Initialize VAD
	vad := audio.NewVAD(
		audio.VADConfig{
			EnergyThreshold: 0.005,                  // Adjust based on testing
			FlushInterval:   310 * time.Millisecond, // Ideally, should fit the min buffer duration
			SilenceDuration: 500 * time.Millisecond, // Detect end of speech
			PauseDuration:   300 * time.Millisecond, // Detect pause within speech
		},
		audio.VADCallbacks{
			OnSpeechStart: func() {
				logger.Info("VAD: Speech started")
				ts.Reset()
			},
			OnSpeechEnd: func() {
				logger.Info("VAD: Speech ended")

				bufferManagerPrimary.Flush()
				bufferManagerSecondary.Flush()

				final := textReconcilier.GetCombinedText()
				fmt.Println("Final Text:", final)
			},
			OnFlush: func(buffer []float32) {
				logger.With("buf_sz", len(buffer)).
					Info("VAD: Buffer flushed")

				data, err := audio.Float32ToPCM(buffer)
				if err != nil {
					logger.
						With("error", err).
						Error(
							"Failed to convert float32 to PCM",
						)
					return
				}

				ts.AddAudio(data)
			},
			OnPause: func() {
				logger.Info("VAD: Speech paused")
				bufferManagerPrimary.Flush()
			},
		},
		logger,
	)

	startedAt := time.Now()
	vad.Start()
	defer vad.Stop()

	// Create input stream
	inputStream, err := audio.NewInputStream(
		logger,
		audioOpts,
		func(buffer []float32) {
			vad.Feed(buffer)
		},
	)
	if err != nil {
		logger.Error("Failed to create input stream", "error", err)
		return
	}

	// Start input stream
	err = inputStream.Start()
	if err != nil {
		logger.Error("Failed to start input stream", "error", err)
		return
	}

	// Handle Ctrl+C to stop
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	// Stop and close input stream
	if err := inputStream.Stop(); err != nil {
		logger.Error("Failed to stop input stream", "error", err)
	}

	if err := inputStream.Close(); err != nil {
		logger.Error("Failed to close input stream", "error", err)
	}

	workflowDuration := time.Since(startedAt)
	totalInferencedTime := tsHandler.GetMetrics().GetTotalDuration()
	logger.Info(
		"Total infered time: " + totalInferencedTime.String(),
	)
	logger.Info(
		"Total workflow duration: " + workflowDuration.String(),
	)

	logger.Info("Program terminated gracefully")
}
