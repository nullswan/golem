package audio

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/gordonklaus/portaudio"
)

type AudioStream struct {
	stream *portaudio.Stream
	logger *slog.Logger
}

type AudioOptions struct {
	SampleRate      float64
	Latency         time.Duration
	FramesPerBuffer int
	Channels        int
	BytesPerSample  int
	BitsPerSample   int
}

func ComputeAudioOptions(opts *AudioOptions) (*AudioOptions, error) {
	if opts == nil {
		return nil, fmt.Errorf("AudioOptions cannot be nil")
	}

	// Get the default input device
	inputDevice, err := portaudio.DefaultInputDevice()
	if err != nil {
		return nil, fmt.Errorf("failed to get default input device: %w", err)
	}

	opts.SampleRate = inputDevice.DefaultSampleRate

	if opts.Latency == 0 {
		opts.Latency = 50 * time.Millisecond
	}

	opts.FramesPerBuffer = int(
		opts.SampleRate * float64(opts.Latency) / float64(time.Second),
	)

	opts.Channels = 1
	opts.BytesPerSample = 2
	opts.BitsPerSample = 16

	return opts, nil
}

func NewInputStream(
	logger *slog.Logger,
	opts *AudioOptions,
	callback func([]float32),
) (*AudioStream, error) {
	// Get the default input device
	inputDevice, err := portaudio.DefaultInputDevice()
	if err != nil {
		return nil, fmt.Errorf("failed to get default input device: %w", err)
	}

	// Compute and validate options
	opts, err = ComputeAudioOptions(opts)
	if err != nil {
		return nil, err
	}

	if opts.FramesPerBuffer > 4096 {
		logger.With("frames_per_buffer", opts.FramesPerBuffer).Warn(
			"FramesPerBuffer seems too high (> 4096)",
		)
	}

	logger = logger.With("component", "audio_stream").
		With("device_name", inputDevice.Name)

	logger.
		With("sample_rate", opts.SampleRate).
		With("frames_per_buffer", opts.FramesPerBuffer).
		With("latency", opts.Latency).
		Info("Using default input device")

	streamParams := portaudio.StreamParameters{
		Input: portaudio.StreamDeviceParameters{
			Device:   inputDevice,
			Channels: 1,
			Latency:  opts.Latency,
		},
		SampleRate:      opts.SampleRate,
		FramesPerBuffer: opts.FramesPerBuffer,
	}

	stream, err := portaudio.OpenStream(
		streamParams,
		func(in []float32, out []float32) {
			callback(in)
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to open stream: %w", err)
	}

	return &AudioStream{
		stream: stream,
		logger: logger,
	}, nil
}

func (a *AudioStream) Start() error {
	a.logger.Info("Starting audio stream")
	return a.stream.Start()
}

func (a *AudioStream) Stop() error {
	a.logger.Info("Stopping audio stream")
	return a.stream.Stop()
}

func (a *AudioStream) Close() error {
	a.logger.Info("Closing audio stream")
	err := a.stream.Close()
	portaudio.Terminate()
	return err
}
