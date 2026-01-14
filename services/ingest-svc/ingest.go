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

func (s *Server) publishEvent(ctx context.Context, event *common.Event) error {
	if s.producer == nil {
		return errors.New("kafka producer not configured")
	}

	start := time.Now()
	defer func() {
		kafkaPublishDuration.Observe(time.Since(start).Seconds())
	}()

	data, err := json.Marshal(event)
	if err != nil {
		return err
	}

	record := &kgo.Record{
		Topic: s.cfg.KafkaTopic,
		Key:   []byte(event.Id),
		Value: data,
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	result := s.producer.ProduceSync(ctx, record)
	return result.FirstErr()
}
