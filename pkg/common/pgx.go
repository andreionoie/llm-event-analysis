package common

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func ConnectPGXPoolWithRetry(ctx context.Context, databaseURL string, logLevel string, attempts int, delay time.Duration) (*pgxpool.Pool, error) {
	var lastErr error
	for i := 0; i < attempts; i++ {
		db, err := ConnectPGXPool(ctx, databaseURL, logLevel)
		if err == nil {
			return db, nil
		}
		lastErr = err
		slog.Warn("failed to connect to database, retrying", "error", err, "attempt", i+1, "max_attempts", attempts)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
	return nil, lastErr
}

func ConnectPGXPool(ctx context.Context, databaseURL string, logLevel string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	cfg.ConnConfig.Tracer = NewPgxTracer(logLevel)
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.Ping(pingCtx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
