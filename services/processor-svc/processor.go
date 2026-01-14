package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/andreionoie/llm-event-analysis/pkg/common"
	"github.com/twmb/franz-go/pkg/kgo"
)

func (s *Server) consume(ctx context.Context) {
	for {
		fetches := s.consumer.PollFetches(ctx)
		if fetches.IsClientClosed() {
			return
		}
		if err := fetches.Err0(); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, kgo.ErrClientClosed) {
				return
			}
			slog.Warn("kafka fetch error", "error", err)
		}

		fetches.EachError(func(topic string, partition int32, err error) {
			if errors.Is(err, context.Canceled) || errors.Is(err, kgo.ErrClientClosed) {
				return
			}
			slog.Warn("kafka fetch error", "error", err, "topic", topic, "partition", partition)
		})

		fetches.EachRecord(func(record *kgo.Record) {
			s.handleRecord(record)
		})
	}
}

func (s *Server) handleRecord(record *kgo.Record) {
	var event common.Event
	if err := json.Unmarshal(record.Value, &event); err != nil {
		slog.Warn("failed to decode event", "error", err, "topic", record.Topic, "partition", record.Partition, "offset", record.Offset)
		return
	}

	if err := event.Validate(); err != nil {
		slog.Warn("invalid event", "error", err, "event_id", event.Id, "topic", record.Topic, "partition", record.Partition, "offset", record.Offset)
		return
	}

	slog.Info(
		"processed event",
		"event_id", event.Id,
		"source", event.Source,
		"severity", event.Severity.String(),
		"type", event.Type,
		"timestamp", event.Timestamp,
		"topic", record.Topic,
		"partition", record.Partition,
		"offset", record.Offset,
	)
}
