package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

const defaultCacheTTL = 30 * time.Minute

func computeCacheKey(req AnalyzeRequest) string {
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
	key := computeCacheKey(req)
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

	key := computeCacheKey(req)
	if key == "" {
		return
	}

	err = s.cache.Set(ctx, key, val, defaultCacheTTL).Err()
	if err != nil {
		slog.Debug("failed to cache response", "error", err, "response", resp)
	}
}
