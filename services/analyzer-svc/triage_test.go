package main

import (
	"reflect"
	"testing"
)

func TestFilterValidBuckets(t *testing.T) {
	valid := map[string]bool{
		"2026-01-01T00:00:00Z": true,
		"2026-01-01T01:00:00Z": true,
	}

	buckets := []BucketRisk{
		{BucketID: "2026-01-01T00:00:00Z", Reason: "suspicious"},
		{BucketID: "hallucinated-by-llm", Reason: "fake"},
		{BucketID: "2026-01-01T01:00:00Z", Reason: "anomaly"},
	}

	got := filterValidBuckets(buckets, valid)
	if len(got) != 2 {
		t.Fatalf("got %d buckets, want 2", len(got))
	}
	for _, b := range got {
		if !valid[b.BucketID] {
			t.Errorf("returned hallucinated bucket: %s", b.BucketID)
		}
	}

	if filterValidBuckets(buckets, nil) != nil {
		t.Error("nil valid map should return nil")
	}
}

func TestFilterValidIDs(t *testing.T) {
	valid := map[string]bool{"evt-1": true, "evt-2": true}
	ids := []string{"evt-1", "hallucinated", "evt-2"}

	got := filterValidIDs(ids, valid)
	want := []string{"evt-1", "evt-2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	if got := filterValidIDs(ids, nil); len(got) != 0 {
		t.Errorf("nil valid map should return empty slice, got %v", got)
	}
}
