package store

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/gobellim/kit/internal/core"
)

func TestChunksIndexBodyUsesDims(t *testing.T) {
	body, err := ChunksIndexBody(768)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatal(err)
	}
	props := parsed["mappings"].(map[string]any)["properties"].(map[string]any)
	vec := props["vector"].(map[string]any)
	if vec["type"] != "dense_vector" || vec["dims"].(float64) != 768 || vec["similarity"] != "cosine" {
		t.Fatalf("vector mapping = %v", vec)
	}
	if props["acl"].(map[string]any)["type"] != "keyword" {
		t.Fatalf("acl must be keyword: %v", props["acl"])
	}

	if _, err := ChunksIndexBody(0); !errors.Is(err, core.ErrInvalidConfig) {
		t.Fatalf("dims 0 must fail with ErrInvalidConfig, got %v", err)
	}
}

func TestTemplatesAreDataStreams(t *testing.T) {
	for name, body := range map[string][]byte{
		"traces":       TracesTemplateBody(),
		"observations": ObservationsTemplateBody(),
	} {
		var parsed map[string]any
		if err := json.Unmarshal(body, &parsed); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, ok := parsed["data_stream"]; !ok {
			t.Fatalf("%s: missing data_stream", name)
		}
	}
}

func TestBulkBodyNDJSON(t *testing.T) {
	chunks := []Chunk{
		{ID: "1", DocID: "d1", Tenant: "t1", ACL: []string{"support"}, Text: "a", Vector: []float32{1, 0}},
		{ID: "2", DocID: "d1", Tenant: "t1", ACL: []string{"support"}, Text: "b", Vector: []float32{0, 1}},
	}
	body, err := ChunksBulkBody(chunks)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("want 4 NDJSON lines, got %d: %s", len(lines), body)
	}
	var action map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &action); err != nil {
		t.Fatal(err)
	}
	meta := action["index"].(map[string]any)
	if meta["_index"] != IndexChunks || meta["_id"] != "1" {
		t.Fatalf("action = %v", meta)
	}
}

func TestACLFilterDenyByDefault(t *testing.T) {
	p := core.Principal{UserID: "u", Tenant: "t1", Roles: []string{"support", "admin"}}
	b, _ := json.Marshal(ACLFilter(p))
	s := string(b)
	if !strings.Contains(s, `"tenant":"t1"`) {
		t.Fatalf("tenant filter missing: %s", s)
	}
	if !strings.Contains(s, `"acl":["support","admin"]`) {
		t.Fatalf("roles filter missing: %s", s)
	}

	empty, _ := json.Marshal(ACLFilter(core.Principal{Tenant: "t1"}))
	if !strings.Contains(string(empty), `"acl":[]`) {
		t.Fatalf("empty roles must still emit an empty terms clause: %s", empty)
	}
}

func TestMSearchNDJSON(t *testing.T) {
	body, err := MSearchNDJSON(SearchQuery{
		Text:      "hello",
		Vector:    []float32{1, 2, 3},
		Principal: core.Principal{Tenant: "t1", Roles: []string{"support"}},
		TopK:      5,
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("want 4 lines (header, knn, header, bm25), got %d", len(lines))
	}
	if !strings.Contains(lines[1], `"knn"`) || !strings.Contains(lines[1], `"num_candidates"`) {
		t.Fatalf("knn line = %s", lines[1])
	}
	if !strings.Contains(lines[3], `"match"`) {
		t.Fatalf("bm25 line = %s", lines[3])
	}
	for _, i := range []int{1, 3} {
		if !strings.Contains(lines[i], `"acl":["support"]`) {
			t.Fatalf("line %d must carry the ACL filter: %s", i, lines[i])
		}
	}
}

func TestParseMSearch(t *testing.T) {
	body := `{"responses":[
		{"hits":{"hits":[{"_id":"a","_score":1.5,"_source":{"text":"x"}}]}},
		{"hits":{"hits":[{"_id":"b","_score":2.0,"_source":{"text":"y"}},{"_id":"a","_score":0.5,"_source":{"text":"x"}}]}}
	]}`
	lists, err := ParseMSearch([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(lists) != 2 || len(lists[0]) != 1 || len(lists[1]) != 2 {
		t.Fatalf("lists = %+v", lists)
	}
	if lists[1][0].ID != "b" {
		t.Fatalf("unexpected first hit: %+v", lists[1][0])
	}
}

func TestRRFFusion(t *testing.T) {
	list1 := []Hit{{ID: "a"}, {ID: "b"}}
	list2 := []Hit{{ID: "b"}, {ID: "c"}}
	got := RRF([][]Hit{list1, list2}, 60, 10)
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}

	if got[0].ID != "b" {
		t.Fatalf("top hit = %s, want b", got[0].ID)
	}
	wantA := 1.0 / 61
	wantB := 1.0/62 + 1.0/61
	if diff := got[1].Score - wantA; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("a score = %v", got[1].Score)
	}
	if diff := got[0].Score - wantB; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("b score = %v", got[0].Score)
	}
}

func TestRRFTopK(t *testing.T) {
	got := RRF([][]Hit{{{ID: "a"}, {ID: "b"}, {ID: "c"}}}, 60, 2)
	if len(got) != 2 {
		t.Fatalf("top-k not applied: %+v", got)
	}
}

type recordingES struct{ paths []string }

func (r *recordingES) Do(_ context.Context, method, path string, body io.Reader) (int, []byte, error) {
	if body != nil {
		_, _ = io.ReadAll(body)
	}
	r.paths = append(r.paths, method+" "+path)
	return 200, []byte(`{}`), nil
}

func TestEnsureSchemaWithoutEmbedder(t *testing.T) {
	es := &recordingES{}
	if err := EnsureSchema(context.Background(), es, 0); err != nil {
		t.Fatal(err)
	}
	if len(es.paths) != 3 {
		t.Fatalf("paths = %v, want ILM + 2 templates", es.paths)
	}
	for _, p := range es.paths {
		if strings.Contains(p, IndexChunks) {
			t.Fatalf("chunks index must not be created without an embedder: %v", es.paths)
		}
	}

	withEmbedder := &recordingES{}
	if err := EnsureSchema(context.Background(), withEmbedder, 768); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range withEmbedder.paths {
		if strings.HasSuffix(p, "/"+IndexChunks) {
			found = true
		}
	}
	if !found {
		t.Fatalf("chunks index missing: %v", withEmbedder.paths)
	}
}
