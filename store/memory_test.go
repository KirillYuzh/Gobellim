package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gobellim/kit/internal/core"
)

func TestMemoryMessageDocument(t *testing.T) {
	doc := MemoryMessage{
		ThreadID: "th1", Tenant: "t1", UserID: "u1", Role: "user",
		Content: "hi", Vector: []float32{1, 0}, EmbedModel: "e5", Seq: 42,
	}.Document()
	if doc[ThreadField] != "th1" || doc[TenantField] != "t1" || doc[UserIDField] != "u1" {
		t.Fatalf("identity fields = %v", doc)
	}
	if doc[RoleField] != "user" || doc[ContentField] != "hi" {
		t.Fatalf("role/content = %v", doc)
	}
	if doc["seq"] != int64(42) {
		t.Fatalf("seq = %v", doc["seq"])
	}
	if _, ok := doc[VectorField]; !ok {
		t.Fatalf("vector missing: %v", doc)
	}
	if doc[EmbedModelField] != "e5" {
		t.Fatalf("embed_model = %v", doc[EmbedModelField])
	}
}

func TestValidateMemoryMessage(t *testing.T) {
	base := MemoryMessage{ThreadID: "th", Tenant: "t", Content: "c", Seq: 1}
	if err := ValidateMemoryMessage(base, 0); err != nil {
		t.Fatalf("valid message rejected: %v", err)
	}

	noSeq := base
	noSeq.Seq = 0
	if err := ValidateMemoryMessage(noSeq, 0); !errors.Is(err, core.ErrInvalidArgs) {
		t.Fatalf("missing seq must fail, got %v", err)
	}

	noThread := base
	noThread.ThreadID = ""
	if err := ValidateMemoryMessage(noThread, 0); !errors.Is(err, core.ErrInvalidArgs) {
		t.Fatalf("missing thread must fail, got %v", err)
	}

	noTenant := base
	noTenant.Tenant = ""
	if err := ValidateMemoryMessage(noTenant, 0); !errors.Is(err, core.ErrInvalidArgs) {
		t.Fatalf("missing tenant must fail, got %v", err)
	}

	badDim := base
	badDim.Vector = []float32{1, 2}
	if err := ValidateMemoryMessage(badDim, 3); !errors.Is(err, core.ErrInvalidArgs) {
		t.Fatalf("dims mismatch must fail, got %v", err)
	}
}

func TestMemoryHistoryBody(t *testing.T) {
	body := MemoryHistoryBody("t1", "u1", "th1", 10)
	raw := string(body)
	for _, want := range []string{`"thread_id":"th1"`, `"tenant":"t1"`, `"user_id":"u1"`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("history body missing %s: %s", want, raw)
		}
	}
	if !strings.Contains(raw, `"seq"`) || !strings.Contains(raw, `"desc"`) {
		t.Fatalf("history body must sort by seq desc: %s", raw)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["size"].(float64) != 10 {
		t.Fatalf("size = %v", parsed["size"])
	}
	if MemoryHistoryBody("t1", "u1", "th1", 0) != nil {
		t.Fatal("zero limit must return nil")
	}
}

func TestMemoryRecallBody(t *testing.T) {
	if _, err := MemoryRecallBody("", "u", []float32{1}, 5, 0); !errors.Is(err, core.ErrInvalidArgs) {
		t.Fatalf("empty tenant must fail, got %v", err)
	}
	if _, err := MemoryRecallBody("t", "u", nil, 5, 0); !errors.Is(err, core.ErrInvalidArgs) {
		t.Fatalf("empty vector must fail, got %v", err)
	}
	body, err := MemoryRecallBody("t1", "u1", []float32{1, 2}, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(body)
	for _, want := range []string{`"knn"`, `"num_candidates":100`, `"user_id":"u1"`, `"tenant":"t1"`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("recall body missing %s: %s", want, raw)
		}
	}
}

func TestMemoryClearBody(t *testing.T) {
	raw := string(MemoryClearBody("t1", "u1", "th1"))
	for _, want := range []string{`"thread_id":"th1"`, `"tenant":"t1"`, `"user_id":"u1"`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("clear body missing %s: %s", want, raw)
		}
	}
}

func TestMemoryWorkingID(t *testing.T) {
	id := MemoryWorkingID("t1", "th1")
	if id != MemoryWorkingID("t1", "th1") {
		t.Fatal("working id must be deterministic")
	}
	if id == MemoryWorkingID("t1", "th2") || id == MemoryWorkingID("t2", "th1") {
		t.Fatal("working id must differ across tenants and threads")
	}
	if strings.ContainsAny(id, "/?# ") {
		t.Fatalf("working id must be path-safe: %q", id)
	}
}

func TestParseMemorySearch(t *testing.T) {
	body := `{"hits":{"hits":[
		{"_score":0.9,"_source":{"role":"user","content":"hi","created_at":"2026-01-02T03:04:05Z"}}
	]}}`
	hits, err := ParseMemorySearch([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Role != "user" || hits[0].Content != "hi" || hits[0].Score != 0.9 {
		t.Fatalf("hits = %+v", hits)
	}
	if hits[0].CreatedAt.IsZero() {
		t.Fatalf("created_at not parsed: %+v", hits[0])
	}

	if _, err := ParseMemorySearch([]byte(`{"error":{"type":"x","reason":"bad"}}`)); !errors.Is(err, core.ErrProvider) {
		t.Fatalf("error response must fail with ErrProvider, got %v", err)
	}
}

func TestMemoryMessagesIndexBody(t *testing.T) {
	var parsed map[string]any
	if err := json.Unmarshal(MemoryMessagesIndexBody(384), &parsed); err != nil {
		t.Fatal(err)
	}
	props := parsed["mappings"].(map[string]any)["properties"].(map[string]any)
	if props["vector"].(map[string]any)["dims"].(float64) != 384 {
		t.Fatalf("vector mapping = %v", props["vector"])
	}
	if props["seq"].(map[string]any)["type"] != "long" {
		t.Fatalf("seq mapping = %v", props["seq"])
	}

	var noVec map[string]any
	if err := json.Unmarshal(MemoryMessagesIndexBody(0), &noVec); err != nil {
		t.Fatal(err)
	}
	props = noVec["mappings"].(map[string]any)["properties"].(map[string]any)
	if _, ok := props["vector"]; ok {
		t.Fatalf("dims 0 must omit the vector field: %v", props)
	}
}
