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

func generateCacheKey(req AnalyzeRequest) string {
	str, err := json.Marshal(req)
	if err != nil {
		panic(err)
	}
	hashBytes := sha256.Sum256(str)
	hashStr := hex.EncodeToString(hashBytes[:])

	return fmt.Sprintf("analyze:%s", hashStr[:12])
}

func (s *Server) getCachedAnalyzeResponse(ctx context.Context, req AnalyzeRequest) *AnalyzeResponse {
	cachedRaw, err := s.cache.Get(ctx, generateCacheKey(req)).Result()
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
	}
	err = s.cache.Set(ctx, generateCacheKey(req), val, defaultCacheTTL).Err()
	if err != nil {
		slog.Debug("failed to cache response", "error", err, "response", resp)
	}
}
