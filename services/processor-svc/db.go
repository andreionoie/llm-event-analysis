package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/andreionoie/llm-event-analysis/pkg/common"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

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
