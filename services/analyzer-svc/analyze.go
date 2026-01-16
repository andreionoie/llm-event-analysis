package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"strings"

	"github.com/andreionoie/llm-event-analysis/pkg/common"
	"github.com/labstack/echo/v4"
	"google.golang.org/genai"
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

	answer, tokensUsed, err := s.analyzeWithLLM(ctx, req.Question, events)
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
		TokensUsed:   tokensUsed,
		SampleEvents: sampleIDs,
	}

	s.cacheAnalyzeResponse(ctx, req, resp)

	return c.JSON(http.StatusOK, resp)
}

func (s *Server) analyzeWithLLM(ctx context.Context, question string, eventList []common.Event) (string, int, error) {
	if s.genai == nil {
		return fmt.Sprintf("Analyzed %d events. (LLM unavailable)", len(eventList)), 0, nil
	}

	if s.prompts == nil {
		return "", 0, fmt.Errorf("prompt library not configured")
	}

	prompt, err := s.prompts.RenderAnalyzePrompt(question, eventList)
	if err != nil {
		return "", 0, err
	}

	genaiConfig := &genai.GenerateContentConfig{}
	prompt.Config.ApplyTo(genaiConfig)
	if prompt.System != "" {
		genaiConfig.SystemInstruction = genai.NewContentFromText(prompt.System, genai.RoleUser)
	}

	slog.Info("calling LLM", "model", prompt.Config.Model, "events", len(eventList))

	resp, err := s.genai.Models.GenerateContent(ctx, prompt.Config.Model, genai.Text(prompt.User), genaiConfig)
	if err != nil {
		return "", 0, err
	}

	return strings.TrimSpace(resp.Text()), 0, nil
}
