package transcription

import (
	"bytes"
	"encoding/binary"
)

// AddWAVHeader adds a WAV header to PCM audio data.
func AddWAVHeader(
	pcmData []byte,
	sampleRate int,
	channels int,
	bitsPerSample int,
) ([]byte, error) {
	var buf bytes.Buffer

	// RIFF header
	if err := binary.Write(&buf, binary.LittleEndian, []byte("RIFF")); err != nil {
		return nil, err
	}

	// ChunkSize: 36 + Subchunk2Size
	chunkSize := 36 + len(pcmData)
	if err := binary.Write(&buf, binary.LittleEndian, uint32(chunkSize)); err != nil {
		return nil, err
	}

	// Format
	if err := binary.Write(&buf, binary.LittleEndian, []byte("WAVE")); err != nil {
		return nil, err
	}

	// Subchunk1 ID
	if err := binary.Write(&buf, binary.LittleEndian, []byte("fmt ")); err != nil {
		return nil, err
	}

	// Subchunk1 Size (16 for PCM)
	if err := binary.Write(&buf, binary.LittleEndian, uint32(16)); err != nil {
		return nil, err
	}

	// AudioFormat (1 for PCM)
	if err := binary.Write(&buf, binary.LittleEndian, uint16(1)); err != nil {
		return nil, err
	}

	// NumChannels
	if err := binary.Write(&buf, binary.LittleEndian, uint16(channels)); err != nil {
		return nil, err
	}

	// SampleRate
	if err := binary.Write(&buf, binary.LittleEndian, uint32(sampleRate)); err != nil {
		return nil, err
	}

	// ByteRate = SampleRate * NumChannels * BitsPerSample/8
	byteRate := sampleRate * channels * bitsPerSample / 8
	if err := binary.Write(&buf, binary.LittleEndian, uint32(byteRate)); err != nil {
		return nil, err
	}

	// BlockAlign = NumChannels * BitsPerSample/8
	blockAlign := channels * bitsPerSample / 8
	if err := binary.Write(&buf, binary.LittleEndian, uint16(blockAlign)); err != nil {
		return nil, err
	}

	// BitsPerSample
	if err := binary.Write(&buf, binary.LittleEndian, uint16(bitsPerSample)); err != nil {
		return nil, err
	}

	// Subchunk2 ID
	if err := binary.Write(&buf, binary.LittleEndian, []byte("data")); err != nil {
		return nil, err
	}

	// Subchunk2 Size
	subchunk2Size := len(pcmData)
	if err := binary.Write(&buf, binary.LittleEndian, uint32(subchunk2Size)); err != nil {
		return nil, err
	}

	// PCM Data
	if _, err := buf.Write(pcmData); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
