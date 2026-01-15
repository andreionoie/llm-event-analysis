package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/andreionoie/llm-event-analysis/pkg/common"
	"github.com/labstack/echo/v4"
)

const (
	defaultEventsLimit    = 50
	defaultSummariesLimit = 24
	maxSummariesLimit     = 200
)

func (s *Server) handleEvents(c echo.Context) error {
	timeRange, err := parseTimeRangeParams(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	limit, err := parseLimitParam(c, defaultEventsLimit, s.cfg.MaxEvents)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	ctx := c.Request().Context()
	events, err := s.fetchEvents(ctx, timeRange, limit)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to fetch events")
	}

	return c.JSON(http.StatusOK, map[string]any{
		"events": events,
		"count":  len(events),
	})
}

func (s *Server) handleSummaries(c echo.Context) error {
	timeRange, err := parseTimeRangeParams(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	limit, err := parseLimitParam(c, defaultSummariesLimit, maxSummariesLimit)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	ctx := c.Request().Context()
	summaries, err := s.fetchSummaries(ctx, timeRange, limit)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to fetch summaries")
	}

	return c.JSON(http.StatusOK, map[string]any{
		"summaries": summaries,
		"count":     len(summaries),
	})
}

func parseTimeRangeParams(c echo.Context) (*common.TimeRange, error) {
	startRaw := strings.TrimSpace(c.QueryParam("start"))
	endRaw := strings.TrimSpace(c.QueryParam("end"))
	if startRaw == "" && endRaw == "" {
		return nil, nil
	}
	if startRaw == "" || endRaw == "" {
		return nil, fmt.Errorf("start and end query params are required together")
	}

	start, err := time.Parse(time.RFC3339, startRaw)
	if err != nil {
		return nil, fmt.Errorf("invalid start time")
	}
	end, err := time.Parse(time.RFC3339, endRaw)
	if err != nil {
		return nil, fmt.Errorf("invalid end time")
	}

	timeRange := &common.TimeRange{Start: start, End: end}
	if err := timeRange.Validate(); err != nil {
		return nil, err
	}
	return timeRange, nil
}

func parseLimitParam(c echo.Context, defaultLimit, maxLimit int) (int, error) {
	limitStr := strings.TrimSpace(c.QueryParam("limit"))
	if limitStr == "" {
		if maxLimit > 0 && defaultLimit > maxLimit {
			return maxLimit, nil
		}
		return defaultLimit, nil
	}

	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		return 0, fmt.Errorf("limit must be a positive integer")
	}

	if maxLimit > 0 && limit > maxLimit {
		return maxLimit, nil
	}

	return limit, nil
}
