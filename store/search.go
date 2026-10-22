package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

const (
	// MaxTopK is the upper bound for SearchQuery.TopK.
	MaxTopK = 1000
	// MaxNumCandidates is the upper bound for SearchQuery.NumCandidates.
	MaxNumCandidates = 10000
)

// Hit is one search result: the document ID, its score, and the raw _source.
type Hit struct {
	ID     string
	Score  float64
	Source json.RawMessage
}

// ACLFilter builds the deny-by-default clause requiring a matching tenant, at
// least one of the principal's roles in acl, and quarantined:false. A missing
// tenant wraps core.ErrInvalidArgs, and nil roles become an empty list that
// matches nothing.
func ACLFilter(p core.Principal) (map[string]any, error) {
	if p.Tenant == "" {
		return nil, fmt.Errorf("store: acl filter requires a tenant: %w", core.ErrInvalidArgs)
	}
	roles := p.Roles
	if roles == nil {
		roles = []string{}
	}
	return map[string]any{
		"bool": map[string]any{
			"filter": []any{
				map[string]any{"term": map[string]any{TenantField: p.Tenant}},
				map[string]any{"terms": map[string]any{ACLField: roles}},

				map[string]any{"term": map[string]any{QuarantineField: false}},
			},
		},
	}, nil
}

// SearchQuery is a hybrid kNN and BM25 search request over chunks, scoped by
// Principal and an optional metadata Filter.
type SearchQuery struct {
	Index         string
	Text          string
	Vector        []float32
	Principal     core.Principal
	TopK          int
	NumCandidates int
	Filter        Filter
	EmbedModel    string
}

// normalized returns a copy of q with the defaults and clamps applied; the
// receiver is left untouched.
func (q SearchQuery) normalized() SearchQuery {
	if q.Index == "" {
		q.Index = IndexChunks
	}
	if q.TopK <= 0 {
		q.TopK = DefaultTopK
	}
	if q.TopK > MaxTopK {
		q.TopK = MaxTopK
	}
	if q.NumCandidates <= 0 {
		q.NumCandidates = q.TopK * 10
		if q.NumCandidates < 100 {
			q.NumCandidates = 100
		}
	}
	if q.NumCandidates > MaxNumCandidates {
		q.NumCandidates = MaxNumCandidates
	}
	return q
}

// effectiveFilter combines the ACL clause with the embed-model clause and the
// compiled metadata filter into one query clause.
func (q SearchQuery) effectiveFilter(filterQuery map[string]any) (map[string]any, error) {
	acl, err := ACLFilter(q.Principal)
	if err != nil {
		return nil, err
	}
	clauses := []any{acl}
	if q.EmbedModel != "" {
		clauses = append(clauses, map[string]any{"term": map[string]any{EmbedModelField: q.EmbedModel}})
	}
	if filterQuery != nil {
		clauses = append(clauses, filterQuery)
	}
	if len(clauses) == 1 {
		return acl, nil
	}
	return map[string]any{
		"bool": map[string]any{
			"filter": clauses,
		},
	}, nil
}

// MSearchNDJSON normalizes q and returns an _msearch body with two sub-queries,
// kNN and BM25, each carrying the ACL clause plus any embed_model and metadata
// filter clauses. TopK defaults to 10 and is clamped to MaxTopK; NumCandidates
// defaults to max(TopK*10, 100) and is clamped to MaxNumCandidates.
func MSearchNDJSON(q SearchQuery) ([]byte, error) {
	q = q.normalized()
	compiled, err := CompileFilter(q.Filter)
	if err != nil {
		return nil, err
	}
	filter, err := q.effectiveFilter(compiled)
	if err != nil {
		return nil, err
	}
	source := []string{"doc_id", TextField, "position"}

	header := map[string]any{"index": q.Index}
	knn := map[string]any{
		"size": q.TopK,
		"knn": map[string]any{
			"field":          VectorField,
			"query_vector":   q.Vector,
			"num_candidates": q.NumCandidates,
			"filter":         filter,
		},
		"_source": source,
	}
	bm25 := map[string]any{
		"size": q.TopK,
		"query": map[string]any{
			"bool": map[string]any{
				"must":   []any{map[string]any{"match": map[string]any{TextField: q.Text}}},
				"filter": []any{filter},
			},
		},
		"_source": source,
	}

	var buf bytes.Buffer
	for _, part := range []map[string]any{header, knn, header, bm25} {
		line, err := json.Marshal(part)
		if err != nil {
			return nil, fmt.Errorf("store: encode msearch line: %w", err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

type msearchResponse struct {
	Responses []msearchItem `json:"responses"`
}

type msearchItem struct {
	Error           json.RawMessage `json:"error"`
	Status          int             `json:"status"`
	TimedOut        bool            `json:"timed_out"`
	TerminatedEarly bool            `json:"terminated_early"`
	Shards          struct {
		Failed int `json:"failed"`
	} `json:"_shards"`
	Hits struct {
		Hits []struct {
			ID     string          `json:"_id"`
			Score  float64         `json:"_score"`
			Source json.RawMessage `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

// ParseMSearch decodes an _msearch response into one hit list per sub-query,
// failing the whole call on any error branch, failed shard, timed-out shard, or
// early-terminated branch; all wrap core.ErrProvider. A partial result is
// never returned as a complete one.
func ParseMSearch(body []byte) ([][]Hit, error) {
	var parsed msearchResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("store: parse msearch response: %w", err)
	}
	lists := make([][]Hit, 0, len(parsed.Responses))
	for i, r := range parsed.Responses {
		if len(r.Error) > 0 {
			return nil, fmt.Errorf("store: msearch response %d failed with status %d: %s: %w",
				i, r.Status, Snippet(r.Error), core.ErrProvider)
		}
		if r.TimedOut {
			return nil, fmt.Errorf("store: msearch response %d timed out: %w", i, core.ErrProvider)
		}
		if r.TerminatedEarly {
			return nil, fmt.Errorf("store: msearch response %d terminated early: %w", i, core.ErrProvider)
		}
		if r.Shards.Failed > 0 {
			return nil, fmt.Errorf("store: msearch response %d had %d failed shards: %w",
				i, r.Shards.Failed, core.ErrProvider)
		}
		hits := make([]Hit, 0, len(r.Hits.Hits))
		for _, h := range r.Hits.Hits {
			hits = append(hits, Hit{ID: h.ID, Score: h.Score, Source: h.Source})
		}
		lists = append(lists, hits)
	}
	return lists, nil
}

// RRF merges ranked hit lists with reciprocal rank fusion, scoring each ID as
// the sum of 1/(rankConstant+rank+1), breaking ties by ascending ID, and
// truncating to topK when topK > 0. A non-positive rankConstant defaults to
// DefaultRRFRankConst.
func RRF(lists [][]Hit, rankConstant, topK int) []Hit {
	if rankConstant <= 0 {
		rankConstant = DefaultRRFRankConst
	}
	scores := make(map[string]float64)
	byID := make(map[string]Hit)
	order := make([]string, 0)

	for _, list := range lists {
		for rank, h := range list {
			if _, seen := byID[h.ID]; !seen {
				order = append(order, h.ID)
			}
			byID[h.ID] = h
			scores[h.ID] += 1.0 / float64(rankConstant+rank+1)
		}
	}

	out := make([]Hit, 0, len(order))
	for _, id := range order {
		h := byID[id]
		h.Score = scores[id]
		out = append(out, h)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	if topK > 0 && len(out) > topK {
		out = out[:topK]
	}
	return out
}
