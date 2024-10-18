package term

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/chzyer/readline"
)

var (
	ErrInputInterrupted = errors.New("input interrupted")
	ErrInputKilled      = errors.New("input killed")
	ErrReadlineInit     = errors.New("error initializing readline")
)

func InitReadline() (*readline.Instance, error) {
	rl, err := readline.NewEx(&readline.Config{
		Prompt:                 ">>> ",
		HistoryFile:            "/dev/null",
		InterruptPrompt:        "Interrupted. Quitting...",
		EOFPrompt:              "Killed. Quitting...",
		AutoComplete:           nil,
		DisableAutoSaveHistory: true,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrReadlineInit, err)
	}
	return rl, nil
}

func NewInputArea() (string, error) {
	rl, err := InitReadline()
	if err != nil {
		return "", err
	}
	defer rl.Close()

	var lines []string

	for {
		line, err := rl.Readline()
		if err != nil {
			if err == readline.ErrInterrupt {
				return "", ErrInputInterrupted
			}
			if err == io.EOF {
				return "", ErrInputKilled
			}
			return "", fmt.Errorf("error reading input: %w", err)
		}

		if strings.TrimSpace(line) == "" {
			break
		}

		lines = append(lines, line)
	}

	return strings.Join(lines, "\n"), nil
}
