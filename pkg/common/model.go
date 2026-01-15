package common

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

//go:generate stringer -type=Severity
type Severity int

const (
	SeverityInfo Severity = iota
	SeverityWarn
	SeverityErr
	SeverityCritical
)

func ParseSeverity(s string) (Severity, error) {
	switch strings.ToLower(s) {
	case "info":
		return SeverityInfo, nil
	case "warn", "warning":
		return SeverityWarn, nil
	case "err", "error":
		return SeverityErr, nil
	case "fatal", "critical":
		return SeverityCritical, nil
	default:
		return SeverityInfo, fmt.Errorf("invalid severity '%s'", s)
	}
}

type Event struct {
	Id        string         `json:"id"`
	Timestamp time.Time      `json:"timestamp"`
	Source    string         `json:"source"`
	Severity  Severity       `json:"severity"`
	Type      string         `json:"type"`
	Payload   map[string]any `json:"payload,omitempty"`
}

func (e *Event) Validate() error {
	if source := strings.TrimSpace(e.Source); source == "" {
		return fmt.Errorf("source is a required field")
	}
	if eventType := strings.TrimSpace(e.Type); eventType == "" {
		return fmt.Errorf("type is a required field")
	}

	return nil
}

func (e *Event) Enrich() {
	if strings.TrimSpace(e.Id) == "" {
		e.Id = randomHexStr(10)
	}

	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
}

func randomHexStr(len int) string {
	key := make([]byte, len)
	_, err := rand.Read(key)
	if err != nil {
		panic("failed to generate random key. this should never happen")
	}
	return hex.EncodeToString(key)
}

type EventSummary struct {
	BucketStart  time.Time      `json:"bucket_start"`
	BucketEnd    time.Time      `json:"bucket_end"`
	TotalCount   int            `json:"total_count"`
	BySeverity   map[string]int `json:"by_severity"`
	ByType       map[string]int `json:"by_type"`
	SampleEvents []Event        `json:"sample_events,omitempty"`
}

type TimeRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

func (r *TimeRange) Validate() error {
	if r.Start.IsZero() || r.End.IsZero() {
		return fmt.Errorf("time range must include start and end")
	}

	if r.Start.After(r.End) {
		return fmt.Errorf("start time must be before end time")
	}

	return nil
}
