package kittest

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/KirillYuzh/Gobellim/guard"
	"github.com/KirillYuzh/Gobellim/trace"
)

// Recorder is an in-memory trace.Sink a test can read back.
func Recorder() *trace.Recorder { return trace.NewRecorder() }

// DetectorContract checks the guard.Detector contract: a non-empty name, and an
// empty input that neither fails nor reports anything.
func DetectorContract(t *testing.T, d guard.Detector) {
	t.Helper()
	if d == nil {
		t.Fatal("kittest: DetectorContract: detector is nil")
	}
	if strings.TrimSpace(d.Name()) == "" {
		t.Error("kittest: DetectorContract: Name() must be non-empty")
	}
	found, err := d.Detect(context.Background(), "")
	if err != nil {
		t.Errorf("kittest: DetectorContract: Detect(\"\") = %v; an empty input must not fail the request", err)
	}
	if len(found) != 0 {
		t.Errorf("kittest: DetectorContract: Detect(\"\") reported %d findings; an empty input has nothing to find", len(found))
	}
}

// SinkContract checks that a trace.Sink takes traces, observations and scores
// without panicking and stays safe under the writer's concurrent calls. Run it
// under -race.
func SinkContract(t *testing.T, s trace.Sink) {
	t.Helper()
	if s == nil {
		t.Fatal("kittest: SinkContract: sink is nil")
	}
	s.Enqueue(trace.Trace{ID: "t1", Name: "chat"})
	s.EnqueueObservation(trace.Observation{ID: "o1", TraceID: "t1", Name: "step", Kind: trace.KindSpan})
	s.EnqueueScore(trace.Score{TraceID: "t1", Name: "quality", Value: 1})

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 16 {
				s.Enqueue(trace.Trace{ID: "t1", Name: "chat"})
				s.EnqueueObservation(trace.Observation{ID: "o1", TraceID: "t1", Name: "step"})
				s.EnqueueScore(trace.Score{TraceID: "t1", Name: "quality"})
			}
		}()
	}
	wg.Wait()
}

// Scope is the neutral access scope a store plugin receives. It is
// deny-by-default (BR-02): an empty scope grants nothing.
type Scope struct {
	Tenant string
	Roles  []string
}

// Document is a stored document with its tenant and ACL entries.
type Document struct {
	ID     string
	Tenant string
	ACL    []string
	Text   string
}

// DocumentIndex is the narrow store surface ScopeIsolation exercises: the store
// port is a raw Elasticsearch client, so the contract binds to this shape.
type DocumentIndex interface {
	Index(ctx context.Context, doc Document) error
	Search(ctx context.Context, scope Scope, query string) ([]Document, error)
}

// ScopeIsolation checks that a store plugin never leaks a document across
// tenants or past an ACL, and that an empty scope returns nothing (BR-02, BR-49).
func ScopeIsolation(t *testing.T, idx DocumentIndex) {
	t.Helper()
	if idx == nil {
		t.Fatal("kittest: ScopeIsolation: index is nil")
	}
	ctx := context.Background()

	for _, doc := range []Document{
		{ID: "a1", Tenant: "alpha", ACL: []string{"reader"}, Text: "alpha handbook"},
		{ID: "b1", Tenant: "beta", ACL: []string{"reader"}, Text: "beta handbook"},
	} {
		if err := idx.Index(ctx, doc); err != nil {
			t.Fatalf("kittest: ScopeIsolation: index %s: %v", doc.ID, err)
		}
	}

	search := func(scope Scope) []Document {
		t.Helper()
		got, err := idx.Search(ctx, scope, "handbook")
		if err != nil {
			t.Fatalf("kittest: ScopeIsolation: search as %+v: %v", scope, err)
		}
		return got
	}

	if got := search(Scope{Tenant: "alpha", Roles: []string{"reader"}}); !sameIDs(got, "a1") {
		t.Errorf("kittest: ScopeIsolation: tenant alpha saw %v, want only [a1]", ids(got))
	}
	if got := search(Scope{Tenant: "beta", Roles: []string{"reader"}}); !sameIDs(got, "b1") {
		t.Errorf("kittest: ScopeIsolation: tenant beta saw %v, want only [b1]", ids(got))
	}
	if got := search(Scope{Tenant: "alpha", Roles: []string{"outsider"}}); len(got) != 0 {
		t.Errorf("kittest: ScopeIsolation: a role outside the ACL saw %v, want nothing", ids(got))
	}
	if got := search(Scope{}); len(got) != 0 {
		t.Errorf("kittest: ScopeIsolation: an empty scope saw %v, want nothing (deny-by-default)", ids(got))
	}
}

func sameIDs(got []Document, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i].ID != want[i] {
			return false
		}
	}
	return true
}

func ids(docs []Document) []string {
	out := make([]string, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.ID)
	}
	return out
}
