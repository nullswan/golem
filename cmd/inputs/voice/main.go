package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/nullswan/golem/internal/input/voiceinput"
	openai "github.com/sashabaranov/go-openai"
)

func main() {
	c := openai.NewClient(
		os.Getenv("OPENAI_API_KEY"),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle interrupt signal to gracefully shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	go func() {
		<-sigChan
		cancel()
	}()

	// Start voice input
	channel := make(chan []byte)
	go voiceinput.Run(ctx, channel)

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-channel:
			switch string(msg) {
			case "[START]":
				fmt.Println("User started speaking.")
			case "[STOP]":
				fmt.Println("User stopped speaking.")
			default:
				fmt.Println("Chunck size: ", len(msg))

				// convert raw audio to wav
				req := openai.AudioRequest{
					Reader:   voiceinput.GetAudioReader(msg),
					Model:    openai.Whisper1,
					FilePath: "audio.wav",
				}

				resp, err := c.CreateTranscription(ctx, req)
				if err != nil {
					fmt.Println("Error creating transcription:", err)
					fmt.Println("Response:", resp)
					continue
				}

				fmt.Println("Transcription:", resp.Text)
			}
		}
	}
}
