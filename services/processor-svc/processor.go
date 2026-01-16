package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/andreionoie/llm-event-analysis/pkg/common"
	"github.com/twmb/franz-go/pkg/kgo"
)

type batchItem struct {
	record *kgo.Record
	event  *common.Event
}

func (s *Server) consume(ctx context.Context, batchCh chan<- batchItem) {
	for {
		fetches := s.consumer.PollFetches(ctx)
		if fetches.IsClientClosed() {
			return
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
			event, dlqReason, dlqErr := s.decodeEvent(record)
			if dlqReason != "" {
				s.publishToDLQ(ctx, record, dlqReason, dlqErr)
				// not skipping sending the event to the channel, since we want its offset to be committed on the topic and not retried
			}
			item := batchItem{
				record: record,
				event:  event,
			}
			select {
			case batchCh <- item:
			case <-ctx.Done():
				return
			}
		}
	}
}

// decodeEvent parses and validates a Kafka record into an Event.
// Returns (event, "", nil) on success, or (nil, reason, error) for DLQ routing.
func (s *Server) decodeEvent(record *kgo.Record) (*common.Event, string, error) {
	var event common.Event
	if err := json.Unmarshal(record.Value, &event); err != nil {
		slog.Warn("failed to decode event", "error", err, "topic", record.Topic, "partition", record.Partition, "offset", record.Offset)
		return nil, DLQReasonUnmarshalFailed, err
	}

	// TODO: assign deterministic hashed ID instead of random inside Enrich()
	event.Enrich()
	if err := event.Validate(); err != nil {
		slog.Warn("invalid event", "error", err, "event_id", event.Id, "topic", record.Topic, "partition", record.Partition, "offset", record.Offset)
		return nil, DLQReasonValidationFailed, err
	}

	return &event, "", nil
}

func (s *Server) processBatches(ctx context.Context, batchCh <-chan batchItem) {
	batch := make([]batchItem, 0, s.cfg.BatchSize)
	flushBatchToDB := func() {
		if len(batch) == 0 {
			return
		}

		events := make([]*common.Event, 0, len(batch))
		records := make([]*kgo.Record, 0, len(batch))
		for _, item := range batch {
			records = append(records, item.record)
			if item.event != nil {
				events = append(events, item.event)
			}
		}

		if err := s.insertEventsBatch(ctx, events); err != nil {
			// reprocess the records since we haven't committed anything yet
			// insertEventsBatch will automatically fallback to row-by-row insert if it fails
			slog.Error("failed to write event batch", "error", err, "count", len(events))
			return
		}

		if err := s.consumer.CommitRecords(ctx, records...); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("failed to commit batch offsets", "error", err, "count", len(records))
			// clear the batch; on reprocessing the records, we will fallback to row-by-row insert
			// which ensures idempotent processing via `ON CONFLICT (id) DO NOTHING`
			batch = batch[:0]
			return
		}

		for _, item := range batch {
			if item.event == nil {
				continue
			}
			slog.Debug(
				"processed event",
				"event_id", item.event.Id,
				"source", item.event.Source,
				"severity", item.event.Severity.String(),
				"type", item.event.Type,
				"timestamp", item.event.Timestamp,
				"topic", item.record.Topic,
				"partition", item.record.Partition,
				"offset", item.record.Offset,
			)
		}

		batch = batch[:0]
	}

	ticker := time.NewTicker(s.cfg.FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case item, ok := <-batchCh:
			// flush and quit when channel got closed
			if !ok {
				flushBatchToDB()
				return
			}
			// flush if we reached target batch size
			batch = append(batch, item)
			if len(batch) >= s.cfg.BatchSize {
				flushBatchToDB()
			}

		case <-ticker.C:
			// flush periodically even if target batch size hasn't been reached
			flushBatchToDB()

		case <-ctx.Done():
			// drain the batch on shutdown and flush
			drain := true
			for drain {
				select {
				case item := <-batchCh:
					batch = append(batch, item)
				default:
					drain = false
				}
			}
			flushBatchToDB()
			return
		}
	}
}
