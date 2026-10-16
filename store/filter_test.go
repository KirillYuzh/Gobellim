package store

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

func compileToJSON(t *testing.T, f Filter) string {
	t.Helper()
	got, err := CompileFilter(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func assertFilterJSON(t *testing.T, f Filter, want string) {
	t.Helper()
	if got := compileToJSON(t, f); got != want {
		t.Fatalf("CompileFilter(%v) = %s, want %s", f, got, want)
	}
}

func TestCompileFilterEmpty(t *testing.T) {
	got, err := CompileFilter(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("empty filter = %v, want nil", got)
	}
	if got := compileToJSON(t, Filter{}); got != "null" {
		t.Fatalf("empty filter JSON = %s", got)
	}
}

func TestCompileFilterScalars(t *testing.T) {
	assertFilterJSON(t, Filter{"doc_id": "d1"}, `{"term":{"doc_id":"d1"}}`)
	assertFilterJSON(t, Filter{"quarantined": true}, `{"term":{"quarantined":true}}`)
	assertFilterJSON(t, Filter{"doc_id": map[string]any{"$ne": "d1"}},
		`{"bool":{"must_not":[{"term":{"doc_id":"d1"}}]}}`)
}

func TestCompileFilterComparison(t *testing.T) {
	assertFilterJSON(t, Filter{"position": map[string]any{"$gte": float64(2)}},
		`{"range":{"position":{"gte":2}}}`)
	assertFilterJSON(t, Filter{"position": map[string]any{"$gte": float64(2), "$lt": float64(10)}},
		`{"range":{"position":{"gte":2,"lt":10}}}`)
	assertFilterJSON(t, Filter{"created_at": map[string]any{"$gt": "2020-01-01T00:00:00Z"}},
		`{"range":{"created_at":{"gt":"2020-01-01T00:00:00Z"}}}`)
}

func TestCompileFilterMembership(t *testing.T) {
	assertFilterJSON(t, Filter{"doc_id": map[string]any{"$in": []string{"a", "b"}}},
		`{"terms":{"doc_id":["a","b"]}}`)
	assertFilterJSON(t, Filter{"doc_id": map[string]any{"$nin": []string{"a"}}},
		`{"bool":{"must_not":[{"terms":{"doc_id":["a"]}}]}}`)
}

func TestCompileFilterExists(t *testing.T) {
	assertFilterJSON(t, Filter{"source": map[string]any{"$exists": true}},
		`{"exists":{"field":"source"}}`)
	assertFilterJSON(t, Filter{"source": map[string]any{"$exists": false}},
		`{"bool":{"must_not":[{"exists":{"field":"source"}}]}}`)
}

func TestCompileFilterLogical(t *testing.T) {
	assertFilterJSON(t, Filter{"$and": []Filter{{"doc_id": "a"}, {"source": "s"}}},
		`{"bool":{"filter":[{"term":{"doc_id":"a"}},{"term":{"source":"s"}}]}}`)
	assertFilterJSON(t, Filter{"$or": []Filter{{"doc_id": "a"}, {"doc_id": "b"}}},
		`{"bool":{"minimum_should_match":1,"should":[{"term":{"doc_id":"a"}},{"term":{"doc_id":"b"}}]}}`)
	assertFilterJSON(t, Filter{"$not": Filter{"doc_id": "a"}},
		`{"bool":{"must_not":[{"term":{"doc_id":"a"}}]}}`)
}

func TestCompileFilterMultipleFields(t *testing.T) {
	assertFilterJSON(t, Filter{"doc_id": "d1", "position": map[string]any{"$gte": float64(2)}},
		`{"bool":{"filter":[{"term":{"doc_id":"d1"}},{"range":{"position":{"gte":2}}}]}}`)
}

func TestCompileFilterErrors(t *testing.T) {
	cases := []Filter{
		{"nope": "x"},
		{"doc_id": map[string]any{"$weird": "x"}},
		{"doc_id": map[string]any{}},
		{"doc_id": map[string]any{"$exists": "yes"}},
		{"$or": "not-an-array"},
		{"$not": "not-a-filter"},
		{"doc_id": map[string]any{"$and": []Filter{{"doc_id": "a"}}}},
	}
	for _, f := range cases {
		if _, err := CompileFilter(f); !errors.Is(err, core.ErrInvalidArgs) {
			t.Fatalf("CompileFilter(%v) error = %v, want ErrInvalidArgs", f, err)
		}
	}
}
