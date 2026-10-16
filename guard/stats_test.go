package guard

import (
	"context"
	"testing"
)

func TestStatsRecordTheLatencyHistogram(t *testing.T) {
	p, err := New(Options{Layers: []Detector{&fakeDetector{name: "L1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Check(context.Background(), RailInput, "hello"); err != nil {
		t.Fatal(err)
	}
	s := p.Stats()
	if len(s.Latency) != len(LatencyBucketMillis)+1 {
		t.Fatalf("Latency = %v, want %d buckets", s.Latency, len(LatencyBucketMillis)+1)
	}
	var total uint64
	for _, n := range s.Latency {
		total += n
	}
	if total != 1 {
		t.Fatalf("latency observations = %d, want 1", total)
	}
	if s.Latency[len(s.Latency)-1] != 0 {
		t.Fatalf("a fast scan must not land in the overflow bucket: %v", s.Latency)
	}
}
