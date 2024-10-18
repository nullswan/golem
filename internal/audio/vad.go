package audio

import (
	"math"
	"sync"
	"time"

	"log/slog"
)

// VADConfig defines configuration options for the VAD
type VADConfig struct {
	EnergyThreshold float64       // Threshold for energy to detect speech
	FlushInterval   time.Duration // Interval for periodic buffer flushing
	SilenceDuration time.Duration // Duration of continuous silence to detect end of speech
	PauseDuration   time.Duration // Duration of brief silence to detect a pause within speech
}

// VADCallbacks defines the callbacks for VAD events
type VADCallbacks struct {
	OnSpeechStart func()
	OnSpeechEnd   func()
	OnPause       func()
	OnFlush       func([]float32)
}

// VAD represents the Voice Activity Detection module
type VAD struct {
	config    VADConfig
	callbacks VADCallbacks
	logger    *slog.Logger

	audioChan chan []float32
	doneChan  chan struct{}
	wg        sync.WaitGroup

	// Internal state
	buffer       []float32
	isSpeaking   bool
	silenceTimer *time.Timer
	pauseTimer   *time.Timer
	mutex        sync.Mutex
}

// NewVAD initializes a new VAD instance
func NewVAD(
	config VADConfig,
	callbacks VADCallbacks,
	logger *slog.Logger,
) *VAD {
	// Set default values if not provided
	if config.EnergyThreshold == 0 {
		config.EnergyThreshold = 0.005 // Increased default threshold for better noise handling
	}
	if config.FlushInterval == 0 {
		config.FlushInterval = 310 * time.Millisecond // Default flush interval
	}
	if config.SilenceDuration == 0 {
		config.SilenceDuration = 500 * time.Millisecond // Duration to detect end of speech
	}
	if config.PauseDuration == 0 {
		config.PauseDuration = 300 * time.Millisecond // Duration to detect a pause within speech
	}

	return &VAD{
		config:    config,
		callbacks: callbacks,
		logger:    logger.With("component", "vad"),
		audioChan: make(chan []float32, 100),
		doneChan:  make(chan struct{}),
		buffer:    make([]float32, 0),
	}
}

// Start begins the VAD processing
func (v *VAD) Start() {
	v.wg.Add(1)
	go v.process()
}

// Stop gracefully stops the VAD processing
func (v *VAD) Stop() {
	close(v.doneChan)
	v.wg.Wait()

	// Ensure timers are stopped to prevent goroutine leaks
	v.mutex.Lock()
	defer v.mutex.Unlock()
	if v.silenceTimer != nil {
		v.silenceTimer.Stop()
	}
	if v.pauseTimer != nil {
		v.pauseTimer.Stop()
	}
}

// Feed inputs an audio buffer into the VAD
func (v *VAD) Feed(buffer []float32) {
	select {
	case v.audioChan <- buffer:
	default:
		v.logger.Warn("Audio channel is full, dropping buffer")
	}
}

// process handles the VAD logic and buffer management
func (v *VAD) process() {
	defer v.wg.Done()

	ticker := time.NewTicker(v.config.FlushInterval)
	defer ticker.Stop()

	for {
		// Locking to safely access timers
		v.mutex.Lock()
		var silenceChan <-chan time.Time
		var pauseChan <-chan time.Time

		if v.silenceTimer != nil {
			silenceChan = v.silenceTimer.C
		}
		if v.pauseTimer != nil {
			pauseChan = v.pauseTimer.C
		}
		v.mutex.Unlock()

		select {
		case <-v.doneChan:
			// Final flush before exiting
			v.flushBuffer()
			return

		case audioBuf := <-v.audioChan:
			energy := calculateRMS(audioBuf)
			v.logger.
				With("energy", energy).
				Debug("Processed buffer")

			v.handleAudioBuffer(audioBuf, energy)

		case <-silenceChan:
			// Silence duration elapsed, end speech
			v.handleSpeechEnd()

		case <-pauseChan:
			// Pause duration elapsed, trigger pause callback
			v.handlePause()

		case <-ticker.C:
			// Periodic flush
			v.flushBuffer()
		}
	}
}

// handleAudioBuffer processes each incoming audio buffer based on its energy
func (v *VAD) handleAudioBuffer(buffer []float32, energy float64) {
	v.mutex.Lock()
	defer v.mutex.Unlock()

	if energy > v.config.EnergyThreshold {
		if !v.isSpeaking {
			v.isSpeaking = true
			v.logger.Debug("Speech started")
			if v.callbacks.OnSpeechStart != nil {
				v.callbacks.OnSpeechStart()
			}
		}
		v.buffer = append(v.buffer, buffer...)

		v.resetSilenceTimer()
		v.resetPauseTimer()
	} else {
		if v.isSpeaking {
			// Start or reset the pause timer
			v.resetPauseTimer()
		}
	}
}

// resetSilenceTimer resets or initializes the silence timer
func (v *VAD) resetSilenceTimer() {
	if v.silenceTimer != nil {
		if !v.silenceTimer.Stop() {
			select {
			case <-v.silenceTimer.C:
			default:
			}
		}
		v.silenceTimer.Reset(v.config.SilenceDuration)
	} else {
		v.silenceTimer = time.NewTimer(v.config.SilenceDuration)
	}
}

// resetPauseTimer resets or initializes the pause timer
func (v *VAD) resetPauseTimer() {
	if v.pauseTimer != nil {
		if !v.pauseTimer.Stop() {
			select {
			case <-v.pauseTimer.C:
			default:
			}
		}
		v.pauseTimer.Reset(v.config.PauseDuration)
	} else {
		v.pauseTimer = time.NewTimer(v.config.PauseDuration)
	}
}

// handleSpeechEnd handles the end of speech event
func (v *VAD) handleSpeechEnd() {
	v.mutex.Lock()
	defer v.mutex.Unlock()
	if v.isSpeaking {
		v.isSpeaking = false

		// Make sure to flush the buffer before ending speech
		v.unsafeFlushBuffer()

		v.logger.Debug("Speech ended")
		if v.callbacks.OnSpeechEnd != nil {
			v.callbacks.OnSpeechEnd()
		}
	}
}

// handlePause handles the pause within speech event
func (v *VAD) handlePause() {
	v.mutex.Lock()
	defer v.mutex.Unlock()
	if v.isSpeaking {
		v.logger.Debug("Pause detected within speech")
		if v.callbacks.OnPause != nil {
			v.callbacks.OnPause()
		}
	}
}

// flushBuffer flushes the current buffer if it's not empty
func (v *VAD) flushBuffer() {
	v.mutex.Lock()
	defer v.mutex.Unlock()

	v.unsafeFlushBuffer()
}

// unsafeFlushBuffer flushes the current buffer without locking
func (v *VAD) unsafeFlushBuffer() {
	if len(v.buffer) > 0 {
		v.logger.Debug("Flushing buffer")
		if v.callbacks.OnFlush != nil {
			v.callbacks.OnFlush(v.buffer)
		}
		v.buffer = nil
	}
}

// calculateRMS computes the Root Mean Square of the audio buffer
func calculateRMS(buffer []float32) float64 {
	var sum float64
	for _, sample := range buffer {
		sum += math.Pow(float64(sample), 2)
	}
	if len(buffer) == 0 {
		return 0
	}
	mean := sum / float64(len(buffer))
	return math.Sqrt(mean)
}
