package common

import (
	"strings"
	"testing"
	"time"
)

func TestParseSeverity(t *testing.T) {
	cases := map[string]Severity{
		"info": SeverityInfo, "INFO": SeverityInfo,
		"warn": SeverityWarn, "warning": SeverityWarn,
		"err": SeverityErr, "error": SeverityErr,
		"critical": SeverityCritical, "fatal": SeverityCritical,
	}
	for input, want := range cases {
		got, err := ParseSeverity(input)
		if err != nil || got != want {
			t.Errorf("ParseSeverity(%q) = %v, %v; want %v, nil", input, got, err, want)
		}
	}

	for _, bad := range []string{"", "garbage", "123"} {
		got, err := ParseSeverity(bad)
		if err == nil {
			t.Errorf("ParseSeverity(%q) should error", bad)
		}
		if got != SeverityInfo {
			t.Errorf("ParseSeverity(%q) = %v on error; want SeverityInfo", bad, got)
		}
	}
}

func TestEventValidate(t *testing.T) {
	valid := Event{Source: "firewall", Type: "blocked"}
	if err := valid.Validate(); err != nil {
		t.Errorf("valid event rejected: %v", err)
	}

	for _, e := range []Event{
		{Source: "", Type: "x"},
		{Source: "x", Type: ""},
		{Source: "   ", Type: "x"},
	} {
		if err := e.Validate(); err == nil {
			t.Errorf("Validate() should reject %+v", e)
		}
	}
}

func TestEventEnrich(t *testing.T) {
	e := &Event{Source: "test", Type: "test"}
	e.Enrich()

	if e.Id == "" || len(e.Id) != 20 {
		t.Errorf("expected 20-char hex ID, got %q", e.Id)
	}
	if e.Timestamp.IsZero() {
		t.Error("Enrich should set timestamp")
	}

	// shouldn't overwrite existing values
	e2 := &Event{Id: "keep-me", Source: "x", Type: "x", Timestamp: time.Unix(1000, 0)}
	e2.Enrich()
	if e2.Id != "keep-me" {
		t.Error("Enrich overwrote existing ID")
	}
	if e2.Timestamp.Unix() != 1000 {
		t.Error("Enrich overwrote existing timestamp")
	}
}

func TestEventEnrich_UniqueIDs(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		e := &Event{Source: "x", Type: "x"}
		e.Enrich()
		if seen[e.Id] {
			t.Fatalf("duplicate ID on iteration %d: %s", i, e.Id)
		}
		seen[e.Id] = true
	}
}

func TestTimeRangeValidate(t *testing.T) {
	now := time.Now()

	if err := (&TimeRange{Start: now, End: now.Add(time.Hour)}).Validate(); err != nil {
		t.Errorf("valid range rejected: %v", err)
	}

	bad := []TimeRange{
		{Start: now.Add(time.Hour), End: now}, // backwards
		{Start: time.Time{}, End: now},        // missing start
		{Start: now, End: time.Time{}},        // missing end
	}
	for _, tr := range bad {
		if err := tr.Validate(); err == nil {
			t.Errorf("Validate() should reject %+v", tr)
		}
	}
}

func TestSeverityString(t *testing.T) {
	// sanity check if the stringer works
	if s := SeverityCritical.String(); !strings.Contains(s, "Critical") {
		t.Errorf("SeverityCritical.String() = %q; want something with Critical", s)
	}
}
