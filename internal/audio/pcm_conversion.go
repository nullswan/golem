package audio

// Float32ToPCM converts a slice of float32 samples to PCM byte data (int16)
func Float32ToPCM(float32Buffer []float32) ([]byte, error) {
	var pcmBuffer []byte
	for _, sample := range float32Buffer {
		// Clipping
		if sample > 1.0 {
			sample = 1.0
		} else if sample < -1.0 {
			sample = -1.0
		}
		// Convert float32 to int16
		intSample := int16(sample * 32767.0)
		pcmBuffer = append(
			pcmBuffer,
			byte(intSample&0xff),
			byte((intSample>>8)&0xff),
		)
	}
	return pcmBuffer, nil
}
