package transcription

import (
	"sync"
	"time"

	"github.com/nullswan/nomi/internal/audio"
)

const flushChanSz = 100

// AudioChunk represents a chunk of audio data with a timestamp.
type AudioChunk struct {
	Data          []byte
	StartDuration time.Duration
	EndDuration   time.Duration
}

// BufferManager manages audio buffering and flushing based on minimum buffer duration.
// It handles overlapping buffers to ensure continuity between chunks.
type BufferManager struct {
	buffer            []byte
	overlapDuration   time.Duration
	minBufferDuration time.Duration
	overlapBytes      int

	sampleRate     int
	channels       int
	bytesPerSample int
	bitsPerSample  int

	mu         sync.Mutex
	flushChan  chan AudioChunk
	baseOffset time.Duration
}

// NewBufferManager creates a new BufferManager instance.
func NewBufferManager(audioOpts *audio.AudioOptions) *BufferManager {
	bm := &BufferManager{
		flushChan: make(
			chan AudioChunk,
			flushChanSz,
		),
		sampleRate:     int(audioOpts.SampleRate),
		channels:       audioOpts.Channels,
		bytesPerSample: audioOpts.BytesPerSample,
		bitsPerSample:  audioOpts.BitsPerSample,
		baseOffset:     0,
	}
	return bm
}

// SetMinBufferDuration sets the minimum buffer duration before flushing.
func (bm *BufferManager) SetMinBufferDuration(duration time.Duration) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.minBufferDuration = duration
}

// SetOverlapDuration sets the duration of audio overlap between chunks.
func (bm *BufferManager) SetOverlapDuration(duration time.Duration) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.overlapDuration = duration
	bm.overlapBytes = bm.computeBytes(duration)
}

// AddAudio appends audio data to the buffer and flushes if the minimum duration is met.
// It also provides a manual Flush capability.
func (bm *BufferManager) AddAudio(audioData []byte) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	bm.buffer = append(bm.buffer, audioData...)
	audioDuration := bm.computeDuration(len(bm.buffer))

	if audioDuration >= bm.minBufferDuration {
		bm.flushBuffer(audioDuration)
	}
}

// Flush forces the buffer to flush its current data.
func (bm *BufferManager) Flush() {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	bm.flushBuffer(0)
}

// [#unsafe] flushBuffer handles the flushing logic to prevent code duplication.
// It assumes that bm.mu is already locked.
func (bm *BufferManager) flushBuffer(bufferDuration time.Duration) {
	if len(bm.buffer) == 0 {
		return
	}

	var duration time.Duration
	if bufferDuration > 0 {
		duration = bufferDuration
	} else {
		// This is the case when we're manually flushing the buffer
		duration = bm.computeDuration(len(bm.buffer))
	}

	start := bm.baseOffset
	end := bm.baseOffset + duration

	chunk := AudioChunk{
		Data:          append([]byte{}, bm.buffer...),
		StartDuration: start,
		EndDuration:   end,
	}

	select {
	case bm.flushChan <- chunk:
		// Retain overlap duration if buffer is not empty
		if bm.overlapBytes < len(bm.buffer) {
			bm.buffer = bm.buffer[len(bm.buffer)-bm.overlapBytes:]
			bm.baseOffset = end - bm.overlapDuration
		} else {
			bm.buffer = bm.buffer[:0]
			bm.baseOffset = end
		}
	default:
		// Flush channel is full; skip flushing to avoid blocking
	}
}

// computeDuration calculates the duration of the buffered audio.
func (bm *BufferManager) computeDuration(bufferLength int) time.Duration {
	if bm.sampleRate == 0 || bm.channels == 0 || bm.bytesPerSample == 0 {
		return 0
	}

	bytesPerSecond := bm.sampleRate * bm.channels * bm.bytesPerSample
	return time.Duration(
		bufferLength,
	) * time.Second / time.Duration(
		bytesPerSecond,
	)
}

// computeBytes calculates the number of bytes for a given duration.
func (bm *BufferManager) computeBytes(duration time.Duration) int {
	if bm.sampleRate == 0 || bm.channels == 0 || bm.bytesPerSample == 0 {
		return 0
	}

	bytesPerSecond := bm.sampleRate * bm.channels * bm.bytesPerSample
	return int(duration.Seconds() * float64(bytesPerSecond))
}

// GetAudio retrieves the next audio chunk from the flush channel.
func (bm *BufferManager) GetAudio() (AudioChunk, bool) {
	audio, ok := <-bm.flushChan
	return audio, ok
}

// Reset resets the BufferManager's baseOffset and clears the buffer.
// It should be called when a new speech session starts.
func (bm *BufferManager) Reset() {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	bm.baseOffset = 0
	bm.buffer = bm.buffer[:0]
}

// Close gracefully shuts down the BufferManager by flushing remaining data and closing channels.
func (bm *BufferManager) Close() {
	bm.mu.Lock()
	// Flush remaining buffer if it exists
	bm.flushBuffer(0)
	bm.mu.Unlock()

	close(bm.flushChan)
}

// IsEmpty returns true if the buffer is empty.
func (bm *BufferManager) IsEmpty() bool {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	return len(bm.buffer) <= bm.overlapBytes
}
