package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/andreionoie/llm-event-analysis/pkg/common"
)

const analyzeResponseTTL = 30 * time.Minute
const triageJobTTL = 30 * time.Minute

func computeAnalyzeCacheKey(req AnalyzeRequest) string {
	str, err := json.Marshal(req)
	if err != nil {
		slog.Debug("failed to marshal request for cache key computation", "error", err, "request", req)
		return ""
	}
	hashBytes := sha256.Sum256(str)
	hashStr := hex.EncodeToString(hashBytes[:])

	return fmt.Sprintf("analyze:%s", hashStr[:12])
}

func (s *Server) getCachedAnalyzeResponse(ctx context.Context, req AnalyzeRequest) *AnalyzeResponse {
	key := computeAnalyzeCacheKey(req)
	if key == "" {
		return nil
	}

	cachedRaw, err := s.cache.Get(ctx, key).Result()
	if err != nil {
		slog.Debug("cache miss", "error", err)
		return nil
	}

	var cached AnalyzeResponse
	if err := json.Unmarshal([]byte(cachedRaw), &cached); err != nil {
		slog.Debug("failed to unmarshal cached response", "error", err, "response", cachedRaw)
		return nil
	}

	return &cached
}

func (s *Server) cacheAnalyzeResponse(ctx context.Context, req AnalyzeRequest, resp AnalyzeResponse) {
	val, err := json.Marshal(resp)
	if err != nil {
		slog.Debug("failed to marshal response for caching", "error", err, "response", resp)
		return
	}

	key := computeAnalyzeCacheKey(req)
	if key == "" {
		return
	}

	err = s.cache.Set(ctx, key, val, analyzeResponseTTL).Err()
	if err != nil {
		slog.Debug("failed to cache response", "error", err, "response", resp)
	}
}

func triageResultsCacheKey(tr common.TimeRange) string {
	data, _ := json.Marshal(tr)
	hash := sha256.Sum256(data)
	return "triage:" + hex.EncodeToString(hash[:])[:12]
}

func triageJobKey(jobID string) string {
	return "triage:job:" + jobID
}

func (s *Server) cacheTriageJob(ctx context.Context, job *TriageJob, cacheKey string) error {
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}

	pipe := s.cache.Pipeline()
	pipe.Set(ctx, triageJobKey(job.ID), data, triageJobTTL)
	pipe.Set(ctx, cacheKey, job.ID, triageJobTTL)
	_, err = pipe.Exec(ctx)
	return err
}

func (s *Server) getCachedTriageJob(ctx context.Context, jobID string) *TriageJob {
	data, err := s.cache.Get(ctx, triageJobKey(jobID)).Result()
	if err != nil {
		return nil
	}

	var job TriageJob
	if err := json.Unmarshal([]byte(data), &job); err != nil {
		return nil
	}
	return &job
}

func (s *Server) getCachedTriageJobByKey(ctx context.Context, cacheKey string) *TriageJob {
	jobID, err := s.cache.Get(ctx, cacheKey).Result()
	if err != nil {
		return nil
	}
	return s.getCachedTriageJob(ctx, jobID)
}
