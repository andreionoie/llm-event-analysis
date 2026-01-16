package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/andreionoie/llm-event-analysis/pkg/common"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"google.golang.org/genai"
)

const (
	triageJobTimeout = 2 * time.Minute
	tier2EventLimit  = 100
)

type TriageJobRequest struct {
	TimeRange common.TimeRange `json:"time_range"`
}

type TriageJob struct {
	ID        string           `json:"id"`
	TimeRange common.TimeRange `json:"time_range"`
	Status    string           `json:"status"` // pending, running, complete, failed
	Error     string           `json:"error,omitempty"`
	CreatedAt time.Time        `json:"created_at"`

	Tier1           *Tier1Result    `json:"tier1,omitempty"`
	Findings        []TriageFinding `json:"findings,omitempty"`
	ScannedEventIDs []string        `json:"scanned_event_ids,omitempty"`
}

type Tier1Result struct {
	Summary    string       `json:"summary"`
	HighRisk   []BucketRisk `json:"high_risk"`
	MediumRisk []BucketRisk `json:"medium_risk"`
	LowRisk    []BucketRisk `json:"low_risk"`
}

type BucketRisk struct {
	BucketID   string  `json:"bucket_id"`
	Reason     string  `json:"reason"`
	Confidence float64 `json:"confidence"`
}

type TriageFinding struct {
	Priority string   `json:"priority"` // P1 to P5; TODO: refactor into enum
	Category string   `json:"category"`
	Summary  string   `json:"summary"`
	EventIDs []string `json:"event_ids"`
}

// schema definitions for structured LLM output
var bucketRiskSchema = &genai.Schema{
	Type: genai.TypeObject,
	Properties: map[string]*genai.Schema{
		"bucket_id":  {Type: genai.TypeString, Description: "RFC3339 timestamp of bucket"},
		"reason":     {Type: genai.TypeString, Description: "Why this risk level"},
		"confidence": {Type: genai.TypeNumber, Description: "Confidence 0.0-1.0"},
	},
	Required: []string{"bucket_id", "reason", "confidence"},
}

var tier1Schema = &genai.Schema{
	Type: genai.TypeObject,
	Properties: map[string]*genai.Schema{
		"summary":     {Type: genai.TypeString, Description: "Brief overall assessment"},
		"high_risk":   {Type: genai.TypeArray, Items: bucketRiskSchema},
		"medium_risk": {Type: genai.TypeArray, Items: bucketRiskSchema},
		"low_risk":    {Type: genai.TypeArray, Items: bucketRiskSchema},
	},
	Required: []string{"summary", "high_risk", "medium_risk", "low_risk"},
}

var tier2Schema = &genai.Schema{
	Type: genai.TypeArray,
	Items: &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"priority": {
				Type:        genai.TypeString,
				Description: "Incident priority level",
				Enum:        []string{"P1", "P2", "P3", "P4", "P5"},
			},
			"category": {Type: genai.TypeString, Description: "Threat category"},
			"summary":  {Type: genai.TypeString, Description: "Finding description"},
			"event_ids": {
				Type:        genai.TypeArray,
				Items:       &genai.Schema{Type: genai.TypeString},
				Description: "Event IDs supporting this finding as evidence",
			},
		},
		Required: []string{"priority", "category", "summary"},
	},
}

func (s *Server) handleCreateTriageJob(c echo.Context) error {
	var req TriageJobRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request")
	}
	if err := req.TimeRange.Validate(); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// idempotency - don't trigger another job for the same time range
	resultsCacheKey := triageResultsCacheKey(req.TimeRange)
	if cached := s.getCachedTriageJobByKey(c.Request().Context(), resultsCacheKey); cached != nil {
		return c.JSON(http.StatusOK, cached)
	}

	job := &TriageJob{
		ID:        uuid.NewString(),
		TimeRange: req.TimeRange,
		Status:    "pending",
		CreatedAt: time.Now().UTC(),
	}

	if err := s.cacheTriageJob(c.Request().Context(), job, resultsCacheKey); err != nil {
		slog.Error("failed to save triage job", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create job")
	}

	// spawn long-running goroutine in the background
	go s.processTriageJob(context.Background(), job, resultsCacheKey)

	return c.JSON(http.StatusAccepted, map[string]string{
		"job_id": job.ID,
		"status": job.Status,
	})
}

func (s *Server) handleGetTriageJob(c echo.Context) error {
	jobID := c.Param("id")
	if jobID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "job_id required")
	}

	cached := s.getCachedTriageJob(c.Request().Context(), jobID)
	if cached == nil {
		return echo.NewHTTPError(http.StatusNotFound, "job not found")
	}

	// basic timeout check; TODO: heartbeat and garbage collector worker
	if cached.Status == "pending" && time.Since(cached.CreatedAt) > triageJobTimeout {
		cached.Status = "failed"
		cached.Error = "job timed out"
		_ = s.cacheTriageJob(c.Request().Context(), cached, triageResultsCacheKey(cached.TimeRange))
	}

	return c.JSON(http.StatusOK, cached)
}

func (s *Server) processTriageJob(ctx context.Context, job *TriageJob, cacheKey string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("triage job panicked", "job_id", job.ID, "panic", r)
			job.Status = "failed"
			job.Error = "internal error"
			_ = s.cacheTriageJob(ctx, job, cacheKey)
		}
	}()

	job.Status = "running"
	_ = s.cacheTriageJob(ctx, job, cacheKey)

	// tier 1: analyze pre-computed summaries from DB
	tier1, validBuckets, err := s.runTriageTier1(ctx, job.TimeRange)
	if err != nil {
		slog.Error("tier 1 failed", "job_id", job.ID, "error", err)
		job.Status = "failed"
		job.Error = "tier 1 analysis failed"
		_ = s.cacheTriageJob(ctx, job, cacheKey)
		return
	}

	// validate bucket IDs against what we actually have, guarding against hallucinations
	tier1.HighRisk = filterValidBuckets(tier1.HighRisk, validBuckets)
	tier1.MediumRisk = filterValidBuckets(tier1.MediumRisk, validBuckets)
	job.Tier1 = tier1

	// tier 2 only if high/medium risk found in structured LLM response
	flagged := append(tier1.HighRisk, tier1.MediumRisk...)
	if len(flagged) == 0 {
		job.Status = "complete"
		_ = s.cacheTriageJob(ctx, job, cacheKey)
		return
	}

	findings, eventIDs, err := s.runTriageTier2(ctx, flagged)
	if err != nil {
		slog.Error("tier 2 failed", "job_id", job.ID, "error", err)
		job.Status = "failed"
		job.Error = "tier 2 analysis failed"
		_ = s.cacheTriageJob(ctx, job, cacheKey)
		return
	}

	job.Findings = findings
	job.ScannedEventIDs = eventIDs
	job.Status = "complete"
	_ = s.cacheTriageJob(ctx, job, cacheKey)
}

func (s *Server) runTriageTier1(ctx context.Context, timeRange common.TimeRange) (*Tier1Result, map[string]bool, error) {
	summaries, err := s.fetchSummaries(ctx, &timeRange, 50)
	if err != nil {
		return nil, nil, err
	}

	if len(summaries) == 0 {
		return &Tier1Result{Summary: "No events found in time range"}, nil, nil
	}

	// build valid bucket set for later validation against hallucination
	validBuckets := make(map[string]bool, len(summaries))
	for _, sum := range summaries {
		validBuckets[sum.BucketStart.Format(time.RFC3339)] = true
	}

	prompt, err := s.prompts.RenderTier1TriagingPrompt(summaries)
	if err != nil {
		return nil, nil, err
	}

	resp, err := s.generateContentWithSchema(ctx, prompt, tier1Schema)
	if err != nil {
		return nil, nil, err
	}

	var result Tier1Result
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		return nil, nil, fmt.Errorf("failed to parse tier 1 response: %w", err)
	}

	return &result, validBuckets, nil
}

func (s *Server) runTriageTier2(ctx context.Context, flagged []BucketRisk) ([]TriageFinding, []string, error) {
	var allEvents []common.Event
	var allIDs []string

	for _, bucket := range flagged {
		bucketTime, err := time.Parse(time.RFC3339, bucket.BucketID)
		if err != nil {
			continue
		}

		// fetch events from DB for this bucket's time window
		timeRange := common.TimeRange{
			Start: bucketTime,
			End:   bucketTime.Add(5 * time.Minute), // TODO: store bucket end as part of prompt
		}
		events, err := s.fetchEvents(ctx, &timeRange, tier2EventLimit)
		if err != nil {
			slog.Warn("failed to fetch events for bucket", "bucket", bucket.BucketID, "error", err)
			continue
		}

		for _, e := range events {
			allEvents = append(allEvents, e)
			allIDs = append(allIDs, e.Id)
		}
	}

	if len(allEvents) == 0 {
		return nil, nil, nil
	}

	prompt, err := s.prompts.RenderTier2TriagingPrompt(allEvents)
	if err != nil {
		return nil, nil, err
	}

	resp, err := s.generateContentWithSchema(ctx, prompt, tier2Schema)
	if err != nil {
		return nil, nil, err
	}

	var findings []TriageFinding
	if err := json.Unmarshal([]byte(resp), &findings); err != nil {
		return nil, nil, fmt.Errorf("failed to parse tier 2 response: %w", err)
	}

	// filter valid event IDs from findings, making sure they are not hallucinated
	validIDs := make(map[string]bool, len(allIDs))
	for _, id := range allIDs {
		validIDs[id] = true
	}
	for i := range findings {
		findings[i].EventIDs = filterValidIDs(findings[i].EventIDs, validIDs)
	}

	return findings, allIDs, nil
}

func filterValidBuckets(buckets []BucketRisk, valid map[string]bool) []BucketRisk {
	if valid == nil {
		return nil
	}
	var out []BucketRisk
	for _, b := range buckets {
		if valid[b.BucketID] {
			out = append(out, b)
		} else {
			slog.Warn("found hallucinated bucket ID", "bucket", b.BucketID)
		}
	}
	return out
}

func filterValidIDs(ids []string, valid map[string]bool) []string {
	var out []string
	for _, id := range ids {
		if valid[id] {
			out = append(out, id)
		} else {
			slog.Warn("found hallucinated event ID", "id", id)
		}
	}
	return out
}
