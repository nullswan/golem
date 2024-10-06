package voiceinput

import (
	"context"
	"io"
	"log"
	"math"
)

// Run starts the voice input loop
// This MUST be run in a separate goroutine
func Run(ctx context.Context, ch chan<- []byte) {
	InitializeAudio()
	defer CleanUpAudio()

	audioChan := make(chan []byte)
	defer close(audioChan)

	// Goroutine to read audio and send to audioChan
	go func() {
		buffer := make(
			[]byte,
			framesPerBuffer*bytesPerSample,
		)
		for {
			select {
			case <-ctx.Done():
				return
			default:
				n, err := ReadAudioChunk(buffer)
				if err != nil && err != io.EOF {
					log.Println("ReadAudioChunk error:", err)
					continue
				}
				if n == 0 {
					continue
				}

				// Make a copy of the buffer to avoid data race
				chunk := make([]byte, n)
				copy(chunk, buffer[:n])
				audioChan <- chunk
			}
		}
	}()

	vad := initializeVAD()
	speaking := false

	chunckAgg := make([]byte, compressedBufferSize, compressedBufferSize)

	for {
		select {
		case <-ctx.Done():
			return
		case chunk, ok := <-audioChan:
			if !ok {
				return
			}

			if isSpeech(vad, chunk) {
				if computeRMS(chunk) < rmsThreshold {
					continue
				}
				if !speaking {
					ch <- []byte(startWord)
					speaking = true
				}
				chunckAgg = append(chunckAgg, chunk...)
				if len(chunckAgg) >= compressedChunkSize {
					flushAudioChunk(ch, &chunckAgg)
				}
			} else {
				if speaking {
					ch <- []byte(stopWord)
					speaking = false

					flushAudioChunk(ch, &chunckAgg)
				}
			}
		}
	}
}

func computeRMS(samples []byte) float64 {
	var sumSquares float64
	for i := 0; i < len(samples); i += 2 {
		if i+1 >= len(samples) {
			break
		}
		sample := int16(samples[i]) | int16(samples[i+1])<<8
		normalizedSample := float64(sample) / 32768.0
		sumSquares += normalizedSample * normalizedSample
	}
	meanSquares := sumSquares / float64(len(samples)/2)
	rms := math.Sqrt(meanSquares)
	return rms
}

func flushAudioChunk(ch chan<- []byte, chunks *[]byte) {
	if len(
		*chunks,
	) <= compressedBufferSize/compressedChunkSize*compressedMinChunks {
		for i := range compressedBufferSize - 1 {
			(*chunks)[i] = 0
		}
		return
	}

	// Flush the chunks
	ch <- *chunks

	// Clear the chunks
	for i := range compressedBufferSize - 1 {
		(*chunks)[i] = 0
	}

	return
}
