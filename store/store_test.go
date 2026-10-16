package store

import (
	"context"
	"encoding/json"
	"errors"
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

func TestTemplateDeclaredFields(t *testing.T) {
	props := func(body []byte) map[string]any {
		var parsed map[string]any
		if err := json.Unmarshal(body, &parsed); err != nil {
			t.Fatal(err)
		}
		return parsed["template"].(map[string]any)["mappings"].(map[string]any)["properties"].(map[string]any)
	}
	traces := props(TracesTemplateBody())
	for _, f := range []string{"kind", "score", "comment", "guard_version", "guard_model", "policy_hash", "text_policy"} {
		if _, ok := traces[f]; !ok {
			t.Fatalf("traces template missing %q", f)
		}
	}
	obs := props(ObservationsTemplateBody())
	for _, f := range []string{"guard_version", "guard_model", "policy_hash", "text_policy", "tags"} {
		if _, ok := obs[f]; !ok {
			t.Fatalf("observations template missing %q", f)
		}
	}
}

func TestBulkBodyNDJSON(t *testing.T) {
	chunks := []Chunk{
		{ID: "1", DocID: "d1", Tenant: "t1", ACL: []string{"support"}, Text: "a", Vector: []float32{1, 0}},
		{ID: "2", DocID: "d1", Tenant: "t1", ACL: []string{"support"}, Text: "b", Vector: []float32{0, 1}},
	}
	body, err := ChunksBulkBody(chunks, 2)
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

func TestValidateChunk(t *testing.T) {
	base := Chunk{ID: "c1", DocID: "d1", Tenant: "t1", Text: "hi", Vector: []float32{1, 0}}
	if err := ValidateChunk(base, 2); err != nil {
		t.Fatalf("valid chunk rejected: %v", err)
	}
	if err := ValidateChunk(base, 3); !errors.Is(err, core.ErrInvalidArgs) {
		t.Fatalf("dims mismatch must fail, got %v", err)
	}
	noVec := base
	noVec.Vector = nil
	if err := ValidateChunk(noVec, 2); !errors.Is(err, core.ErrInvalidArgs) {
		t.Fatalf("non-quarantine chunk without vector must fail, got %v", err)
	}
	q := Chunk{ID: "q1", DocID: "d1", Tenant: "t1", Quarantine: true}
	if err := ValidateChunk(q, 2); err != nil {
		t.Fatalf("clean quarantine chunk rejected: %v", err)
	}
	q.Text = "leak"
	if err := ValidateChunk(q, 2); !errors.Is(err, core.ErrInvalidArgs) {
		t.Fatalf("quarantined chunk with text must fail, got %v", err)
	}
	q.Text = ""
	q.Vector = []float32{1, 0}
	if err := ValidateChunk(q, 2); !errors.Is(err, core.ErrInvalidArgs) {
		t.Fatalf("quarantined chunk with vector must fail, got %v", err)
	}
}

func TestQuarantinedChunkStoresNoContent(t *testing.T) {
	doc := Chunk{ID: "c1", DocID: "d1", Tenant: "t1", Quarantine: true}.Document()
	if _, ok := doc[TextField]; ok {
		t.Fatalf("quarantined document must not carry text: %v", doc)
	}
	if _, ok := doc[VectorField]; ok {
		t.Fatalf("quarantined document must not carry a vector: %v", doc)
	}
	if doc["quarantined"] != true {
		t.Fatalf("quarantined flag lost: %v", doc)
	}
}

func TestACLFilterDenyByDefault(t *testing.T) {
	p := core.Principal{UserID: "u", Tenant: "t1", Roles: []string{"support", "admin"}}
	acl, err := ACLFilter(p)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(acl)
	s := string(b)
	if !strings.Contains(s, `"tenant":"t1"`) {
		t.Fatalf("tenant filter missing: %s", s)
	}
	if !strings.Contains(s, `"acl":["support","admin"]`) {
		t.Fatalf("roles filter missing: %s", s)
	}

	emptyACL, err := ACLFilter(core.Principal{Tenant: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	empty, _ := json.Marshal(emptyACL)
	if !strings.Contains(string(empty), `"acl":[]`) {
		t.Fatalf("empty roles must still emit an empty terms clause: %s", empty)
	}

	if _, err := ACLFilter(core.Principal{Roles: []string{"support"}}); !errors.Is(err, core.ErrInvalidArgs) {
		t.Fatalf("empty tenant must fail, got %v", err)
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
	if strings.Contains(lines[1], `"k":`) {
		t.Fatalf("knn line must not contain a k member: %s", lines[1])
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

func TestMSearchNDJSONRejectsEmptyTenant(t *testing.T) {
	if _, err := MSearchNDJSON(SearchQuery{Text: "x", Vector: []float32{1}}); !errors.Is(err, core.ErrInvalidArgs) {
		t.Fatalf("empty tenant must fail, got %v", err)
	}
}

func TestMSearchNDJSONAppliesFilter(t *testing.T) {
	body, err := MSearchNDJSON(SearchQuery{
		Text:      "hello",
		Vector:    []float32{1, 2, 3},
		Principal: core.Principal{Tenant: "t1", Roles: []string{"support"}},
		TopK:      5,
		Filter:    Filter{"doc_id": "d1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	for _, i := range []int{1, 3} {
		if !strings.Contains(lines[i], `"doc_id":"d1"`) {
			t.Fatalf("line %d must carry the user filter: %s", i, lines[i])
		}
		if !strings.Contains(lines[i], `"acl":["support"]`) {
			t.Fatalf("line %d must still carry the ACL filter: %s", i, lines[i])
		}
	}
}

func TestNormalizedClamps(t *testing.T) {
	got, err := SearchQuery{TopK: 5000, NumCandidates: 99999}.normalized()
	if err != nil {
		t.Fatal(err)
	}
	if got.TopK != MaxTopK {
		t.Fatalf("TopK = %d, want %d", got.TopK, MaxTopK)
	}
	if got.NumCandidates != MaxNumCandidates {
		t.Fatalf("NumCandidates = %d, want %d", got.NumCandidates, MaxNumCandidates)
	}

	def, err := SearchQuery{}.normalized()
	if err != nil {
		t.Fatal(err)
	}
	if def.TopK != DefaultTopK || def.NumCandidates != 100 {
		t.Fatalf("defaults = TopK %d NumCandidates %d", def.TopK, def.NumCandidates)
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

func TestParseMSearchBranchError(t *testing.T) {
	body := `{"responses":[
		{"error":{"root_cause":[{"type":"illegal_argument_exception","reason":"bad knn"}],"type":"illegal_argument_exception","reason":"bad knn"},"status":400},
		{"hits":{"hits":[]}}
	]}`
	_, err := ParseMSearch([]byte(body))
	if !errors.Is(err, core.ErrProvider) {
		t.Fatalf("branch error must produce an ErrProvider, got %v", err)
	}
}

func TestParseMSearchShardFailure(t *testing.T) {
	body := `{"responses":[{"_shards":{"total":2,"successful":1,"failed":1},"hits":{"hits":[]}}]}`
	_, err := ParseMSearch([]byte(body))
	if !errors.Is(err, core.ErrProvider) {
		t.Fatalf("failed shard must produce an ErrProvider, got %v", err)
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

func TestRRFTieBreak(t *testing.T) {
	lists := [][]Hit{
		{{ID: "b"}, {ID: "a"}},
		{{ID: "a"}, {ID: "b"}},
	}
	got := RRF(lists, 60, 10)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Score != got[1].Score {
		t.Fatalf("scores not equal: %v vs %v", got[0].Score, got[1].Score)
	}
	if got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("tie-break order = %s, %s, want a, b", got[0].ID, got[1].ID)
	}
}

func TestRRFTopK(t *testing.T) {
	got := RRF([][]Hit{{{ID: "a"}, {ID: "b"}, {ID: "c"}}}, 60, 2)
	if len(got) != 2 {
		t.Fatalf("top-k not applied: %+v", got)
	}
}

func TestRequests(t *testing.T) {
	req, err := JSONRequest("POST", "/idx/_search", map[string]any{"query": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if req.ContentType != "application/json" || string(req.Body) != `{"query":"x"}` {
		t.Fatalf("JSONRequest = %+v", req)
	}

	nd := NDJSONRequest("POST", "/_bulk", []byte("{}\n"))
	if nd.ContentType != "application/x-ndjson" {
		t.Fatalf("NDJSONRequest content type = %q", nd.ContentType)
	}

	raw := RawRequest("PUT", "/x", nil, "")
	if raw.ContentType != "" || raw.Body != nil {
		t.Fatalf("RawRequest = %+v", raw)
	}
}

func TestEffectivePath(t *testing.T) {
	cases := []struct {
		path    string
		refresh string
		want    string
	}{
		{"/idx/_search", "", "/idx/_search"},
		{"/idx/_search", "wait_for", "/idx/_search?refresh=wait_for"},
		{"/idx/_search?pretty", "true", "/idx/_search?pretty&refresh=true"},
	}
	for _, c := range cases {
		got := Request{Path: c.path, Refresh: c.refresh}.EffectivePath()
		if got != c.want {
			t.Fatalf("EffectivePath(%q, %q) = %q, want %q", c.path, c.refresh, got, c.want)
		}
	}
}

type recordingES struct{ paths []string }

func (r *recordingES) Do(_ context.Context, req Request) (int, []byte, error) {
	r.paths = append(r.paths, req.Method+" "+req.EffectivePath())
	return 200, []byte(`{}`), nil
}

func TestEnsureSchemaWithoutEmbedder(t *testing.T) {
	es := &recordingES{}
	if err := EnsureSchema(context.Background(), es, 0); err != nil {
		t.Fatal(err)
	}
	if len(es.paths) != 5 {
		t.Fatalf("paths = %v, want ILM + 2 templates + 2 memory indices", es.paths)
	}
	for _, p := range es.paths {
		if strings.Contains(p, IndexChunks) {
			t.Fatalf("chunks index must not be created without an embedder: %v", es.paths)
		}
	}
	for _, want := range []string{IndexMemoryMessages, IndexMemoryWorking} {
		found := false
		for _, p := range es.paths {
			if strings.HasSuffix(p, "/"+want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("index %s missing: %v", want, es.paths)
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

func TestChunkDocumentCarriesEmbedModel(t *testing.T) {
	doc := Chunk{ID: "c1", DocID: "d1", Tenant: "t1", Text: "x", Vector: []float32{1, 0}, EmbedModel: "e5"}.Document()
	if doc[EmbedModelField] != "e5" {
		t.Fatalf("embed_model missing from the chunk document: %v", doc)
	}

	q := Chunk{ID: "q1", DocID: "d1", Tenant: "t1", Quarantine: true, EmbedModel: "e5"}.Document()
	if _, ok := q[EmbedModelField]; ok {
		t.Fatalf("quarantined chunk must not carry embed_model: %v", q)
	}
}

func TestMSearchNDJSONFiltersByEmbedModel(t *testing.T) {
	body, err := MSearchNDJSON(SearchQuery{
		Text:       "hello",
		Vector:     []float32{1, 2, 3},
		Principal:  core.Principal{Tenant: "t1", Roles: []string{"support"}},
		TopK:       5,
		EmbedModel: "intfloat/multilingual-e5-small",
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	for _, i := range []int{1, 3} {
		if !strings.Contains(lines[i], `"embed_model":"intfloat/multilingual-e5-small"`) {
			t.Fatalf("line %d must carry the embed model filter: %s", i, lines[i])
		}
		if !strings.Contains(lines[i], `"acl":["support"]`) {
			t.Fatalf("line %d must still carry the ACL filter: %s", i, lines[i])
		}
	}
}
