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
		e.Id = randomHexStr(32)
	}

	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
}

func randomHexStr(len int) string {
	key := make([]byte, len)
	rand.Read(key)
	return hex.EncodeToString(key)
}
