package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/gobellim/kit/internal/core"
)

type Hit struct {
	ID     string
	Score  float64
	Source json.RawMessage
}

// ACLFilter is the deny-by-default ACL clause: the document tenant must match
// the principal tenant and its acl list must contain one of the principal's
// roles. An empty role list matches nothing.
func ACLFilter(p core.Principal) map[string]any {
	roles := p.Roles
	if roles == nil {

		roles = []string{}
	}
	return map[string]any{
		"bool": map[string]any{
			"filter": []any{
				map[string]any{"term": map[string]any{TenantField: p.Tenant}},
				map[string]any{"terms": map[string]any{ACLField: roles}},

				map[string]any{"term": map[string]any{"quarantined": false}},
			},
		},
	}
}

type SearchQuery struct {
	Index         string
	Text          string
	Vector        []float32
	Principal     core.Principal
	TopK          int
	NumCandidates int
}

func (q SearchQuery) normalized() SearchQuery {
	if q.Index == "" {
		q.Index = IndexChunks
	}
	if q.TopK <= 0 {
		q.TopK = DefaultTopK
	}
	if q.NumCandidates <= 0 {
		q.NumCandidates = q.TopK * 10
		if q.NumCandidates < 100 {
			q.NumCandidates = 100
		}
	}
	return q
}

// MSearchNDJSON renders the _msearch body: a kNN query followed by a BM25
// query, both carrying the principal ACL filter.
func MSearchNDJSON(q SearchQuery) ([]byte, error) {
	q = q.normalized()
	source := []string{"doc_id", TextField, "position"}

	header := map[string]any{"index": q.Index}
	knn := map[string]any{
		"size": q.TopK,
		"query": map[string]any{
			"knn": map[string]any{
				"field":          VectorField,
				"query_vector":   q.Vector,
				"k":              q.TopK,
				"num_candidates": q.NumCandidates,
				"filter":         ACLFilter(q.Principal),
			},
		},
		"_source": source,
	}
	bm25 := map[string]any{
		"size": q.TopK,
		"query": map[string]any{
			"bool": map[string]any{
				"must":   []any{map[string]any{"match": map[string]any{TextField: q.Text}}},
				"filter": []any{ACLFilter(q.Principal)},
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
	Responses []struct {
		Hits struct {
			Hits []struct {
				ID     string          `json:"_id"`
				Score  float64         `json:"_score"`
				Source json.RawMessage `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	} `json:"responses"`
}

func ParseMSearch(body []byte) ([][]Hit, error) {
	var parsed msearchResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("store: parse msearch response: %w", err)
	}
	lists := make([][]Hit, 0, len(parsed.Responses))
	for _, r := range parsed.Responses {
		hits := make([]Hit, 0, len(r.Hits.Hits))
		for _, h := range r.Hits.Hits {
			hits = append(hits, Hit{ID: h.ID, Score: h.Score, Source: h.Source})
		}
		lists = append(lists, hits)
	}
	return lists, nil
}

// RRF fuses ranked hit lists with Reciprocal Rank Fusion and returns the top-k,
// scoring each id as sum over lists of 1 / (rankConstant + rank + 1).
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
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if topK > 0 && len(out) > topK {
		out = out[:topK]
	}
	return out
}
