package main

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/andreionoie/llm-event-analysis/pkg/common"
)

func (s *Server) fetchEvents(ctx context.Context, timeRange *common.TimeRange, limit int) ([]common.Event, error) {
	query := `SELECT id, timestamp, source, severity, event_type, payload FROM events`
	var args []any

	if timeRange != nil {
		query += ` WHERE timestamp >= $1 AND timestamp <= $2`
		args = append(args, timeRange.Start, timeRange.End)
	}

	query += ` ORDER BY timestamp DESC LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, limit)

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]common.Event, 0, limit)
	for rows.Next() {
		var event common.Event
		var sev int
		var payload []byte
		if err := rows.Scan(&event.Id, &event.Timestamp, &event.Source, &sev, &event.Type, &payload); err != nil {
			return nil, err
		}
		event.Severity = common.Severity(sev)
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &event.Payload); err != nil {
				return nil, err
			}
		}
		events = append(events, event)
	}

	return events, rows.Err()
}

func (s *Server) fetchSummaries(ctx context.Context, timeRange *common.TimeRange, limit int) ([]common.EventSummary, error) {
	query := `SELECT bucket_start, bucket_end, total_count, by_severity, by_type, sample_events FROM event_summaries`
	var args []any

	if timeRange != nil {
		query += ` WHERE bucket_start >= $1 AND bucket_start <= $2`
		args = append(args, timeRange.Start, timeRange.End)
	}

	query += ` ORDER BY bucket_start DESC LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, limit)

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	summaries := make([]common.EventSummary, 0, limit)
	for rows.Next() {
		var summary common.EventSummary
		var bySevJSON []byte
		var byTypeJSON []byte
		var samplesJSON []byte
		if err := rows.Scan(
			&summary.BucketStart,
			&summary.BucketEnd,
			&summary.TotalCount,
			&bySevJSON,
			&byTypeJSON,
			&samplesJSON,
		); err != nil {
			return nil, err
		}

		if len(bySevJSON) > 0 {
			if err := json.Unmarshal(bySevJSON, &summary.BySeverity); err != nil {
				return nil, err
			}
		}
		if summary.BySeverity == nil {
			summary.BySeverity = map[string]int{}
		}

		if len(byTypeJSON) > 0 {
			if err := json.Unmarshal(byTypeJSON, &summary.ByType); err != nil {
				return nil, err
			}
		}
		if summary.ByType == nil {
			summary.ByType = map[string]int{}
		}

		if len(samplesJSON) > 0 {
			if err := json.Unmarshal(samplesJSON, &summary.SampleEvents); err != nil {
				return nil, err
			}
		}

		summaries = append(summaries, summary)
	}

	return summaries, rows.Err()
}
