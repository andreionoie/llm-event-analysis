package main

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/andreionoie/llm-event-analysis/pkg/common"
)

func (s *Server) fetchEvents(ctx context.Context, timeRange *TimeRange, limit int) ([]common.Event, error) {
	query := `SELECT id, timestamp, source, severity, event_type, payload FROM events`
	args := []any{}

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
