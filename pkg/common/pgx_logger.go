package common

import (
	"context"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/tracelog"
)

type pgxSlogLogger struct {
	logger *slog.Logger
}

func NewPgxTracer(levelStr string) *tracelog.TraceLog {
	return &tracelog.TraceLog{
		Logger:   &pgxSlogLogger{logger: slog.Default().With("component", "postgres")},
		LogLevel: pgxLogLevelFromString(levelStr),
	}
}

func (l *pgxSlogLogger) Log(ctx context.Context, level tracelog.LogLevel, msg string, data map[string]any) {
	if l == nil || l.logger == nil {
		return
	}

	slogLevel := slog.LevelInfo
	switch level {
	case tracelog.LogLevelTrace, tracelog.LogLevelDebug:
		slogLevel = slog.LevelDebug
	case tracelog.LogLevelInfo:
		slogLevel = slog.LevelInfo
	case tracelog.LogLevelWarn:
		slogLevel = slog.LevelWarn
	case tracelog.LogLevelError:
		slogLevel = slog.LevelError
	}

	if len(data) == 0 {
		l.logger.Log(ctx, slogLevel, msg)
		return
	}

	fields := make([]any, 0, len(data)*2)
	for key, value := range data {
		fields = append(fields, key, value)
	}
	l.logger.Log(ctx, slogLevel, msg, fields...)
}

func pgxLogLevelFromString(levelStr string) tracelog.LogLevel {
	switch strings.ToLower(levelStr) {
	case "trace":
		return tracelog.LogLevelTrace
	case "debug":
		return tracelog.LogLevelDebug
	case "warn", "warning":
		return tracelog.LogLevelWarn
	case "error":
		return tracelog.LogLevelError
	case "none":
		return tracelog.LogLevelNone
	default:
		return tracelog.LogLevelInfo
	}
}
