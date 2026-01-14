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

		iter := fetches.RecordIter()
		for !iter.Done() {
			record := iter.Next()
			if !s.handleRecord(ctx, record) {
				break
			}
			if err := s.consumer.CommitRecords(ctx, record); err != nil {
				if !errors.Is(err, context.Canceled) {
					slog.Error("failed to commit offset", "error", err, "topic", record.Topic, "partition", record.Partition, "offset", record.Offset)
				}
				break
			}
		}
	}
}

func (s *Server) handleRecord(ctx context.Context, record *kgo.Record) bool {
	var event common.Event
	if err := json.Unmarshal(record.Value, &event); err != nil {
		slog.Warn("failed to decode event", "error", err, "topic", record.Topic, "partition", record.Partition, "offset", record.Offset)
		return true
	}

	event.Enrich()
	if err := event.Validate(); err != nil {
		slog.Warn("invalid event", "error", err, "event_id", event.Id, "topic", record.Topic, "partition", record.Partition, "offset", record.Offset)
		return true
	}

	if err := s.insertEvent(ctx, &event); err != nil {
		slog.Error(
			"failed to insert event",
			"error", err,
			"event_id", event.Id,
			"topic", record.Topic,
			"partition", record.Partition,
			"offset", record.Offset,
		)
		return false
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
	return true
}
