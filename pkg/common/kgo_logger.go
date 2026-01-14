package common

import (
	"context"
	"log/slog"
	"strings"

	"github.com/twmb/franz-go/pkg/kgo"
)

type KgoSlogLogger struct {
	logger *slog.Logger
	level  kgo.LogLevel
}

func NewKgoSlogLogger(logger *slog.Logger, level kgo.LogLevel) *KgoSlogLogger {
	if logger == nil {
		logger = slog.Default()
	}
	return &KgoSlogLogger{
		logger: logger,
		level:  level,
	}
}

func (l *KgoSlogLogger) Level() kgo.LogLevel {
	return l.level
}

func (l *KgoSlogLogger) Log(level kgo.LogLevel, msg string, keyvals ...any) {
	if level == kgo.LogLevelNone {
		return
	}

	slogLevel := slog.LevelInfo
	switch level {
	case kgo.LogLevelDebug:
		slogLevel = slog.LevelDebug
	case kgo.LogLevelWarn:
		slogLevel = slog.LevelWarn
	case kgo.LogLevelError:
		slogLevel = slog.LevelError
	}

	l.logger.Log(context.Background(), slogLevel, msg, keyvals...)
}

func KgoLogLevelFromString(levelStr string) kgo.LogLevel {
	switch strings.ToLower(levelStr) {
	case "debug":
		return kgo.LogLevelDebug
	case "warn", "warning":
		return kgo.LogLevelWarn
	case "error":
		return kgo.LogLevelError
	case "none":
		return kgo.LogLevelNone
	default:
		return kgo.LogLevelInfo
	}
}
