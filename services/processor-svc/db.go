package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/andreionoie/llm-event-analysis/pkg/common"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

const summarySampleLimit = 5

func registerDBMetrics(db *pgxpool.Pool) (*sql.DB, error) {
	sqlDB := stdlib.OpenDBFromPool(db)
	prometheus.MustRegister(collectors.NewDBStatsCollector(sqlDB, "processor_db"))
	return sqlDB, nil
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

	result, err := s.db.Exec(
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
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return nil
	}

	if err := s.updateSummary(ctx, event); err != nil {
		slog.Warn("failed to update event summary", "error", err, "event_id", event.Id)
	}
	return nil
}

func (s *Server) updateSummary(ctx context.Context, event *common.Event) error {
	if s.db == nil {
		return errors.New("database not configured")
	}
	if s.cfg.SummaryBucket <= 0 {
		return nil
	}

	bucketStart := event.Timestamp.UTC().Truncate(s.cfg.SummaryBucket)
	bucketEnd := bucketStart.Add(s.cfg.SummaryBucket)

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for {
		var totalCount int
		var bySevJSON []byte
		var byTypeJSON []byte
		var samplesJSON []byte

		err = tx.QueryRow(
			ctx,
			`SELECT total_count, by_severity, by_type, sample_events
			 FROM event_summaries
			 WHERE bucket_start = $1
			 FOR NO KEY UPDATE`,
			bucketStart,
		).Scan(&totalCount, &bySevJSON, &byTypeJSON, &samplesJSON)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				bySeverity := map[string]int{event.Severity.String(): 1}
				byType := map[string]int{event.Type: 1}
				samples := []common.Event{*event}

				bySevJSON, err = json.Marshal(bySeverity)
				if err != nil {
					return err
				}
				byTypeJSON, err = json.Marshal(byType)
				if err != nil {
					return err
				}
				samplesJSON, err = json.Marshal(samples)
				if err != nil {
					return err
				}

				_, err = tx.Exec(
					ctx,
					`INSERT INTO event_summaries
					 (bucket_start, bucket_end, total_count, by_severity, by_type, sample_events)
					 VALUES ($1, $2, $3, $4, $5, $6)`,
					bucketStart,
					bucketEnd,
					1,
					bySevJSON,
					byTypeJSON,
					samplesJSON,
				)
				if err != nil {
					var pgErr *pgconn.PgError
					if errors.As(err, &pgErr) && pgErr.Code == "23505" {
						continue
					}
					return err
				}
				return tx.Commit(ctx)
			}
			return err
		}

		totalCount++

		var bySeverity map[string]int
		if err := json.Unmarshal(bySevJSON, &bySeverity); err != nil {
			return err
		}
		if bySeverity == nil {
			bySeverity = make(map[string]int)
		}
		bySeverity[event.Severity.String()]++

		var byType map[string]int
		if err := json.Unmarshal(byTypeJSON, &byType); err != nil {
			return err
		}
		if byType == nil {
			byType = make(map[string]int)
		}
		byType[event.Type]++

		var samples []common.Event
		if err := json.Unmarshal(samplesJSON, &samples); err != nil {
			return err
		}
		if len(samples) < summarySampleLimit {
			samples = append(samples, *event)
		}

		bySevJSON, err = json.Marshal(bySeverity)
		if err != nil {
			return err
		}
		byTypeJSON, err = json.Marshal(byType)
		if err != nil {
			return err
		}
		samplesJSON, err = json.Marshal(samples)
		if err != nil {
			return err
		}

		_, err = tx.Exec(
			ctx,
			`UPDATE event_summaries
			 SET bucket_end = $2,
			     total_count = $3,
			     by_severity = $4,
			     by_type = $5,
			     sample_events = $6
			 WHERE bucket_start = $1`,
			bucketStart,
			bucketEnd,
			totalCount,
			bySevJSON,
			byTypeJSON,
			samplesJSON,
		)
		if err != nil {
			return err
		}

		return tx.Commit(ctx)
	}
}
