package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/andreionoie/llm-event-analysis/pkg/common"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

func connectDBWithRetry(ctx context.Context, databaseURL string, logLevel string, attempts int, delay time.Duration) (*pgxpool.Pool, error) {
	var lastErr error
	for i := 0; i < attempts; i++ {
		db, err := connectDB(ctx, databaseURL, logLevel)
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

func connectDB(ctx context.Context, databaseURL string, logLevel string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	cfg.ConnConfig.Tracer = common.NewPgxTracer(logLevel)
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

func (s *Server) insertEvent(ctx context.Context, event *common.Event) error {
	if s.db == nil {
		return errors.New("database not configured")
	}

	payloadJSON := []byte("{}")
	if event.Payload != nil {
		var err error
		payloadJSON, err = json.Marshal(event.Payload)
		if err != nil {
			return err
		}
	}

	_, err := s.db.Exec(
		ctx,
		`INSERT INTO events (id, timestamp, source, severity, event_type, payload)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (id) DO NOTHING`,
		event.Id,
		event.Timestamp,
		event.Source,
		int(event.Severity),
		event.Type,
		payloadJSON,
	)
	return err
}

func registerDBMetrics(db *pgxpool.Pool) (*sql.DB, error) {
	sqlDB := stdlib.OpenDBFromPool(db)
	prometheus.MustRegister(collectors.NewDBStatsCollector(sqlDB, "processor_db"))
	return sqlDB, nil
}
