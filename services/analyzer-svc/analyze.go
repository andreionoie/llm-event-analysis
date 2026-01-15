package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/andreionoie/llm-event-analysis/pkg/common"
	"github.com/labstack/echo/v4"
	"google.golang.org/genai"
)

type AnalyzeRequest struct {
	Question  string            `json:"question"`
	MaxEvents int               `json:"max_events,omitempty"`
	TimeRange *common.TimeRange `json:"time_range,omitempty"`
}

type AnalyzeResponse struct {
	Answer       string   `json:"answer"`
	EventsUsed   int      `json:"events_used"`
	TokensUsed   int      `json:"tokens_used,omitempty"`
	Cached       bool     `json:"cached,omitempty"`
	SampleEvents []string `json:"sample_events,omitempty"`
}

func (s *Server) handleAnalyze(c echo.Context) error {
	var req AnalyzeRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if strings.TrimSpace(req.Question) == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "question is required")
	}
	if req.TimeRange != nil {
		if err := req.TimeRange.Validate(); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
	}

	maxEvents := req.MaxEvents
	if maxEvents <= 0 || maxEvents > s.cfg.MaxEvents {
		maxEvents = s.cfg.MaxEvents
	}

	ctx := c.Request().Context()
	events, err := s.fetchEvents(ctx, req.TimeRange, maxEvents)
	if err != nil {
		slog.Error("failed to fetch events", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to fetch events")
	}

	answer, tokensUsed, err := s.analyzeWithLLM(ctx, req.Question, events)
	if err != nil {
		slog.Error("analysis failed", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "analysis failed")
	}

	sampleIDs := make([]string, 0, min(5, len(events)))
	for i := 0; i < min(5, len(events)); i++ {
		sampleIDs = append(sampleIDs, events[i].Id)
	}

	return c.JSON(http.StatusOK, AnalyzeResponse{
		Answer:       answer,
		EventsUsed:   len(events),
		TokensUsed:   tokensUsed,
		SampleEvents: sampleIDs,
	})
}

func (s *Server) analyzeWithLLM(ctx context.Context, question string, eventList []common.Event) (string, int, error) {
	if s.genai == nil {
		return fmt.Sprintf("Analyzed %d events. (LLM unavailable)", len(eventList)), 0, nil
	}

	prompt := renderPrompt(question, eventList)
	slog.Info("calling LLM", "model", s.cfg.GeminiModel, "events", len(eventList))

	resp, err := s.genai.Models.GenerateContent(ctx, s.cfg.GeminiModel, genai.Text(prompt), nil)
	if err != nil {
		return "", 0, err
	}

	return strings.TrimSpace(resp.Text()), 0, nil
}

func renderPrompt(question string, eventList []common.Event) string {
	var b strings.Builder
	b.WriteString("Answer the question using only the events provided. If the answer is not supported, say so.\n\n")
	b.WriteString("Events:\n")
	for _, e := range eventList {
		b.WriteString("- [")
		b.WriteString(e.Timestamp.Format(time.RFC3339))
		b.WriteString("] ")
		b.WriteString(e.Severity.String())
		b.WriteString(" | ")
		b.WriteString(e.Source)
		b.WriteString(" | ")
		b.WriteString(e.Type)
		b.WriteString(" | ")
		b.WriteString(truncatePayload(e.Payload, 200))
		b.WriteString("\n")
	}
	b.WriteString("\nQuestion:\n")
	b.WriteString(question)
	return b.String()
}

func truncatePayload(payload map[string]any, maxLen int) string {
	if len(payload) == 0 {
		return "{}"
	}
	data, _ := json.Marshal(payload)
	s := string(data)
	if len(s) > maxLen {
		return s[:maxLen-3] + "..."
	}
	return s
}
