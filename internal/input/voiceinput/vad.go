package voiceinput

import (
	"log"

	"github.com/maxhawkins/go-webrtcvad"
)

const vadMode = 2 // TODO(nullswan): Make this configurable

func initializeVAD() *webrtcvad.VAD {
	vad, err := webrtcvad.New()
	if err != nil {
		log.Fatalf("Failed to initialize VAD: %v", err)
	}

	// Set aggressiveness mode (0-3). Higher values are more aggressive in filtering out non-speech.
	err = vad.SetMode(vadMode)
	if err != nil {
		log.Fatalf("Failed to set VAD mode: %v", err)
	}

	return vad
}

func isSpeech(vad *webrtcvad.VAD, audio []byte) bool {
	speech, err := vad.Process(int(sampleRate), audio)
	if err != nil {
		log.Println("VAD processing error:", err)
		return false
	}
	return speech
}
