package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

func TestDeleteByDocBody(t *testing.T) {
	body, err := DeleteByDocBody("d1", "t1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	if !strings.Contains(s, `"doc_id":"d1"`) || !strings.Contains(s, `"tenant":"t1"`) {
		t.Fatalf("delete body = %s", s)
	}

	if _, err := DeleteByDocBody("", "t1"); !errors.Is(err, core.ErrInvalidArgs) {
		t.Fatalf("empty doc id must fail, got %v", err)
	}
	if _, err := DeleteByDocBody("d1", ""); !errors.Is(err, core.ErrInvalidArgs) {
		t.Fatalf("empty tenant must fail, got %v", err)
	}
}

func TestParseDeleteByQuery(t *testing.T) {
	deleted, err := ParseDeleteByQuery([]byte(`{"deleted":3,"version_conflicts":0,"failures":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 3 {
		t.Fatalf("deleted = %d, want 3", deleted)
	}

	if _, err := ParseDeleteByQuery([]byte(`{"deleted":1,"failures":[{"cause":"boom"}]}`)); !errors.Is(err, core.ErrProvider) {
		t.Fatalf("failures must fail, got %v", err)
	}
	if _, err := ParseDeleteByQuery([]byte(`{"deleted":1,"version_conflicts":2}`)); !errors.Is(err, core.ErrProvider) {
		t.Fatalf("version conflicts must fail, got %v", err)
	}
	if _, err := ParseDeleteByQuery([]byte(`not json`)); err == nil {
		t.Fatal("invalid json must fail")
	}
}

func TestPruneBody(t *testing.T) {
	before := time.Now().UTC().Add(-time.Hour).Add(-time.Second)
	after := time.Now().UTC().Add(-time.Hour).Add(time.Second)

	var parsed struct {
		Query struct {
			Range map[string]struct {
				LT string `json:"lt"`
			} `json:"range"`
		} `json:"query"`
	}
	body, err := PruneBody(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatal(err)
	}
	cutoff, err := time.Parse(time.RFC3339Nano, parsed.Query.Range["@timestamp"].LT)
	if err != nil {
		t.Fatal(err)
	}
	if cutoff.Before(before) || cutoff.After(after) {
		t.Fatalf("cutoff %s not within [%s, %s]", cutoff, before, after)
	}
}

func TestSnippetCutsOnRuneBoundary(t *testing.T) {
	body := []byte(strings.Repeat("\u20ac", 70)) // 210 bytes, three bytes per rune
	got := Snippet(body)
	if !utf8.ValidString(got) {
		t.Fatalf("snippet must be valid UTF-8: %q", got)
	}
	if len(got) > 200 {
		t.Fatalf("snippet too long: %d bytes", len(got))
	}
	if len(got)%3 != 0 {
		t.Fatalf("snippet must end on a rune boundary, got %d bytes", len(got))
	}
	if !strings.HasPrefix(string(body), got) {
		t.Fatalf("snippet must be a prefix of the body")
	}
	if s := Snippet([]byte{0xff, 0xfe}); s != "" {
		t.Fatalf("invalid bytes must be dropped, got %q", s)
	}
}
