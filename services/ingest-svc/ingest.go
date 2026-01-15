package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/andreionoie/llm-event-analysis/pkg/common"
	"github.com/labstack/echo/v4"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/twmb/franz-go/pkg/kgo"
)

type IngestRequest struct {
	Source   string         `json:"source"`
	Severity string         `json:"severity"`
	Type     string         `json:"type"`
	Payload  map[string]any `json:"payload,omitempty"`
}

func (r *IngestRequest) ToEvent() (*common.Event, error) {
	sev, err := common.ParseSeverity(r.Severity)
	if err != nil {
		sev = common.SeverityInfo
	}

	event := &common.Event{
		Source:   r.Source,
		Severity: sev,
		Type:     r.Type,
		Payload:  r.Payload,
	}
	event.Enrich()

	if err := event.Validate(); err != nil {
		return nil, err
	}
	return event, nil
}

type IngestResponse struct {
	ID        string `json:"id"`
	Accepted  bool   `json:"accepted"`
	Timestamp string `json:"timestamp,omitempty"`
	Error     string `json:"error,omitempty"`
}

type IngestBatchRequest struct {
	Events []IngestRequest `json:"events"`
}

type IngestBatchResponse struct {
	Accepted int              `json:"accepted"`
	Events   []IngestResponse `json:"events"`
}

var (
	eventsIngested = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ingest_events_total",
			Help: "Total number of events ingested, partitioned by status",
		},
		[]string{"status"},
	)
	kafkaPublishDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "ingest_kafka_publish_seconds",
			Help:    "Time spent publishing events to Kafka",
			Buckets: prometheus.DefBuckets,
		},
	)
)

func (s *Server) handleIngest(c echo.Context) error {
	var req IngestRequest
	if err := c.Bind(&req); err != nil {
		eventsIngested.WithLabelValues("rejected").Inc()
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	event, err := req.ToEvent()
	if err != nil {
		eventsIngested.WithLabelValues("rejected").Inc()
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	if err := s.publishEvent(c.Request().Context(), event); err != nil {
		slog.Error("failed to publish event", "error", err, "event_id", event.Id)
		eventsIngested.WithLabelValues("error").Inc()
		return echo.NewHTTPError(http.StatusServiceUnavailable, "failed to queue event")
	}

	eventsIngested.WithLabelValues("accepted").Inc()
	return c.JSON(http.StatusAccepted, IngestResponse{
		ID:        event.Id,
		Accepted:  true,
		Timestamp: event.Timestamp.Format(time.RFC3339),
	})
}

func (s *Server) handleIngestBatch(c echo.Context) error {
	var req IngestBatchRequest
	if err := c.Bind(&req); err != nil {
		eventsIngested.WithLabelValues("rejected").Inc()
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if len(req.Events) == 0 {
		eventsIngested.WithLabelValues("rejected").Inc()
		return echo.NewHTTPError(http.StatusBadRequest, "events is required")
	}

	responses := make([]IngestResponse, len(req.Events))
	records := make([]*kgo.Record, 0, len(req.Events))
	recordIndex := make(map[*kgo.Record]int, len(req.Events))

	for i, item := range req.Events {
		event, err := item.ToEvent()
		if err != nil {
			eventsIngested.WithLabelValues("rejected").Inc()
			responses[i] = IngestResponse{
				Accepted: false,
				Error:    err.Error(),
			}
			continue
		}

		data, err := json.Marshal(event)
		if err != nil {
			eventsIngested.WithLabelValues("error").Inc()
			responses[i] = IngestResponse{
				Accepted: false,
				Error:    "failed to encode event",
			}
			continue
		}

		record := &kgo.Record{
			Topic: s.cfg.KafkaTopic,
			Key:   []byte(event.Id),
			Value: data,
		}
		records = append(records, record)
		recordIndex[record] = i
		responses[i] = IngestResponse{
			ID:        event.Id,
			Accepted:  true,
			Timestamp: event.Timestamp.Format(time.RFC3339),
		}
	}

	if len(records) == 0 {
		return c.JSON(http.StatusAccepted, IngestBatchResponse{
			Accepted: 0,
			Events:   responses,
		})
	}

	results, err := s.publishRecords(c.Request().Context(), records)
	if err != nil {
		slog.Error("failed to publish events", "error", err, "count", len(records))
		eventsIngested.WithLabelValues("error").Add(float64(len(records)))
		for _, record := range records {
			if idx, ok := recordIndex[record]; ok {
				responses[idx].Accepted = false
				responses[idx].Error = "failed to publish event"
			}
		}
		return c.JSON(http.StatusServiceUnavailable, IngestBatchResponse{
			Accepted: 0,
			Events:   responses,
		})
	}

	accepted := 0
	for _, result := range results {
		idx, ok := recordIndex[result.Record]
		if !ok {
			continue
		}
		if result.Err != nil {
			responses[idx].Accepted = false
			responses[idx].Timestamp = ""
			responses[idx].Error = result.Err.Error()
			eventsIngested.WithLabelValues("error").Inc()
			continue
		}
		accepted++
		eventsIngested.WithLabelValues("accepted").Inc()
	}

	return c.JSON(http.StatusAccepted, IngestBatchResponse{
		Accepted: accepted,
		Events:   responses,
	})
}

func (s *Server) publishEvent(ctx context.Context, event *common.Event) error {
	results, err := s.publishEvents(ctx, []*common.Event{event})
	if err != nil {
		return err
	}
	return results.FirstErr()
}

func (s *Server) publishEvents(ctx context.Context, events []*common.Event) (kgo.ProduceResults, error) {
	records := make([]*kgo.Record, 0, len(events))
	for _, event := range events {
		data, err := json.Marshal(event)
		if err != nil {
			return nil, err
		}

		records = append(records, &kgo.Record{
			Topic: s.cfg.KafkaTopic,
			Key:   []byte(event.Id),
			Value: data,
		})
	}

	return s.publishRecords(ctx, records)
}

func (s *Server) publishRecords(ctx context.Context, records []*kgo.Record) (kgo.ProduceResults, error) {
	if s.producer == nil {
		return nil, errors.New("kafka producer not configured")
	}

	start := time.Now()
	defer func() {
		kafkaPublishDuration.Observe(time.Since(start).Seconds())
	}()

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	return s.producer.ProduceSync(ctx, records...), nil
}
