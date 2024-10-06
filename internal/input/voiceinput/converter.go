package voiceinput

import (
	"bytes"
	"encoding/binary"
	"io"
)

func GetAudioReader(data []byte) io.Reader {
	var header [44]byte

	// RIFF header
	copy(header[0:4], []byte("RIFF"))
	binary.LittleEndian.PutUint32(
		header[4:8],
		uint32(36+len(data)),
	) // ChunkSize
	copy(header[8:12], []byte("WAVE"))

	// fmt subchunk
	copy(header[12:16], []byte("fmt "))
	binary.LittleEndian.PutUint32(
		header[16:20],
		16,
	) // Subchunk1Size
	binary.LittleEndian.PutUint16(
		header[20:22],
		1,
	) // AudioFormat (PCM)
	binary.LittleEndian.PutUint16(
		header[22:24],
		uint16(channels),
	) // NumChannels
	binary.LittleEndian.PutUint32(
		header[24:28],
		uint32(sampleRate),
	) // SampleRate
	byteRate := sampleRate * channels * 2                          // ByteRate
	binary.LittleEndian.PutUint32(header[28:32], uint32(byteRate)) // ByteRate
	blockAlign := channels * 2                                     // BlockAlign
	binary.LittleEndian.PutUint16(
		header[32:34],
		uint16(blockAlign),
	) // BlockAlign
	binary.LittleEndian.PutUint16(
		header[34:36],
		16,
	) // BitsPerSample

	// data subchunk
	copy(header[36:40], []byte("data"))
	binary.LittleEndian.PutUint32(
		header[40:44],
		uint32(len(data)),
	) // Subchunk2Size

	buf := bytes.NewBuffer(header[:])
	buf.Write(data)

	return buf
}
