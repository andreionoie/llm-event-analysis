package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/twmb/franz-go/pkg/kgo"
)

type DLQRecord struct {
	OriginalTopic     string    `json:"original_topic"`
	OriginalPartition int32     `json:"original_partition"`
	OriginalOffset    int64     `json:"original_offset"`
	OriginalKey       string    `json:"original_key,omitempty"`
	OriginalValueB64  string    `json:"original_value_b64"` // base64-encoded to handle any malformed input
	FailedAt          time.Time `json:"failed_at"`
	Reason            string    `json:"reason"`
	Error             string    `json:"error,omitempty"`
}

var dlqMessagesTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "processor",
		Name:      "dlq_messages_total",
		Help:      "Total number of messages sent to the dead letter queue",
	},
	[]string{"reason"},
)

const (
	DLQReasonUnmarshalFailed  = "unmarshal_failed"
	DLQReasonValidationFailed = "validation_failed"
)

func (s *Server) publishToDLQ(ctx context.Context, record *kgo.Record, reason string, err error) {
	if s.dlqProducer == nil {
		slog.Debug("DLQ producer not configured, skipping",
			"reason", reason,
			"error", err,
			"offset", record.Offset,
			"partition", record.Partition,
		)
		return
	}

	errStr := ""
	if err != nil {
		errStr = err.Error()
	}

	dlqRecord := DLQRecord{
		OriginalTopic:     record.Topic,
		OriginalPartition: record.Partition,
		OriginalOffset:    record.Offset,
		OriginalKey:       string(record.Key),
		OriginalValueB64:  base64.StdEncoding.EncodeToString(record.Value),
		FailedAt:          time.Now().UTC(),
		Reason:            reason,
		Error:             errStr,
	}

	payload, marshalErr := json.Marshal(dlqRecord)
	if marshalErr != nil {
		slog.Warn("failed to marshal DLQ record",
			"error", marshalErr,
			"original_error", err,
			"offset", record.Offset,
		)
		return
	}

	dlqMsg := &kgo.Record{
		Topic: s.cfg.DLQTopic,
		Key:   record.Key,
		Value: payload,
	}

	s.dlqProducer.Produce(ctx, dlqMsg, func(r *kgo.Record, produceErr error) {
		if produceErr != nil {
			slog.Warn("failed to produce to DLQ",
				"error", produceErr,
				"original_offset", record.Offset,
				"reason", reason,
			)
			return
		}
		dlqMessagesTotal.WithLabelValues(reason).Inc()
		slog.Debug("message sent to DLQ",
			"reason", reason,
			"original_offset", record.Offset,
			"original_partition", record.Partition,
			"dlq_offset", r.Offset,
		)
	})
}
