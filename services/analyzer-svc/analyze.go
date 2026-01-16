package main

import (
	"log/slog"
	"math/rand"
	"net/http"
	"strings"

	"github.com/andreionoie/llm-event-analysis/pkg/common"
	"github.com/labstack/echo/v4"
)

const eventsSampleLimit = 5

type AnalyzeRequest struct {
	Question  string            `json:"question"`
	MaxEvents int               `json:"max_events,omitempty"`
	TimeRange *common.TimeRange `json:"time_range,omitempty"`
}

type AnalyzeResponse struct {
	Answer       string   `json:"answer"`
	EventsUsed   int      `json:"events_used"`
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

	ctx := c.Request().Context()

	if cached := s.getCachedAnalyzeResponse(ctx, req); cached != nil {
		cached.Cached = true
		return c.JSON(http.StatusOK, *cached)
	}

	maxEvents := req.MaxEvents
	if maxEvents <= 0 || maxEvents > s.cfg.MaxEvents {
		maxEvents = s.cfg.MaxEvents
	}

	events, err := s.fetchEvents(ctx, req.TimeRange, maxEvents)
	if err != nil {
		slog.Error("failed to fetch events", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to fetch events")
	}

	prompt, err := s.prompts.RenderAnalyzePrompt(req.Question, events)
	if err != nil {
		slog.Error("analysis failed", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "analysis failed")
	}

	answer, err := s.generateContent(ctx, prompt)
	if err != nil {
		slog.Error("analysis failed", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "analysis failed")
	}

	samplesCount := min(eventsSampleLimit, len(events))
	sampleIDs := make([]string, samplesCount)
	// randomize samples
	rand.Shuffle(len(events), func(i, j int) {
		events[i], events[j] = events[j], events[i]
	})
	for i := range samplesCount {
		sampleIDs[i] = events[i].Id
	}

	resp := AnalyzeResponse{
		Answer:       answer,
		EventsUsed:   len(events),
		SampleEvents: sampleIDs,
	}

	s.cacheAnalyzeResponse(ctx, req, resp)

	return c.JSON(http.StatusOK, resp)
}
