package logger

import (
	"log/slog"
	"os"
	"sync"
)

type Logger = slog.Logger

var (
	logger *slog.Logger
	once   sync.Once
)

func initInstance() {
	level := slog.LevelError
	if os.Getenv("DEBUG") != "" {
		level = slog.LevelDebug
	}

	loggerHandlerOpts := &slog.HandlerOptions{
		Level: level,
	}

	logger = slog.New(
		slog.NewTextHandler(os.Stdout, loggerHandlerOpts),
	)
}

func Init() *slog.Logger {
	once.Do(func() {
		initInstance()
	})

	return logger
}
