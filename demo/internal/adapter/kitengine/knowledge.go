package kitengine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/gobellim/kit"
	"github.com/gobellim/kit/demo/internal/domain"
	"github.com/gobellim/kit/store"
)

const maxListedDocs = 1000

func (e *Engine) Create(ctx context.Context, p domain.Principal, doc domain.KnowledgeDoc) error {
	ctx = kit.WithPrincipal(ctx, kitPrincipal(p))
	return e.app.Ingest(ctx, kit.Doc{ID: doc.ID, Text: doc.Text, ACL: doc.ACL, Source: doc.Title})
}

func (e *Engine) Update(ctx context.Context, p domain.Principal, doc domain.KnowledgeDoc) error {
	ctx = kit.WithPrincipal(ctx, kitPrincipal(p))
	if err := e.app.Delete(ctx, doc.ID); err != nil {
		return translate(err)
	}
	return e.Create(ctx, p, doc)
}

func (e *Engine) Delete(ctx context.Context, p domain.Principal, id string) error {
	ctx = kit.WithPrincipal(ctx, kitPrincipal(p))
	return translate(e.app.Delete(ctx, id))
}

func (e *Engine) Search(ctx context.Context, p domain.Principal, query string, topK int) ([]domain.KnowledgeHit, error) {
	ctx = kit.WithPrincipal(ctx, kitPrincipal(p))

	hits, err := e.app.Search(ctx, kit.SearchRequest{Query: query, TopK: topK})
	if err != nil {
		return nil, translate(err)
	}
	titles, err := e.titles(ctx, p)
	if err != nil {
		return nil, err
	}

	out := make([]domain.KnowledgeHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, domain.KnowledgeHit{
			DocID:    h.DocID,
			Title:    titles[h.DocID],
			Text:     h.Text,
			Position: h.Position,
		})
	}
	return out, nil
}

func (e *Engine) List(ctx context.Context, p domain.Principal) ([]domain.KnowledgeDoc, error) {
	buckets, err := e.docBuckets(ctx, p)
	if err != nil {
		return nil, err
	}

	out := make([]domain.KnowledgeDoc, 0, len(buckets))
	for _, b := range buckets {
		out = append(out, domain.KnowledgeDoc{ID: b.Key, Title: b.title(), Chunks: b.DocCount})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (e *Engine) Get(ctx context.Context, p domain.Principal, id string) (domain.KnowledgeDoc, error) {
	body := map[string]any{
		"size":    maxListedDocs,
		"query":   e.chunkFilter(p, id),
		"sort":    []any{map[string]any{"position": map[string]any{"order": "asc"}}},
		"_source": []string{"text", "position", "acl", "source"},
	}
	var res struct {
		Hits struct {
			Hits []struct {
				Source struct {
					Text     string   `json:"text"`
					Position int      `json:"position"`
					ACL      []string `json:"acl"`
					Source   string   `json:"source"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := e.esSearch(ctx, body, &res); err != nil {
		return domain.KnowledgeDoc{}, err
	}
	if len(res.Hits.Hits) == 0 {
		return domain.KnowledgeDoc{}, fmt.Errorf("document %q: %w", id, domain.ErrNotFound)
	}

	parts := make([]string, 0, len(res.Hits.Hits))
	for _, h := range res.Hits.Hits {
		parts = append(parts, h.Source.Text)
	}
	first := res.Hits.Hits[0].Source
	return domain.KnowledgeDoc{
		ID:     id,
		Title:  first.Source,
		Text:   strings.Join(parts, "\n\n"),
		ACL:    first.ACL,
		Chunks: len(res.Hits.Hits),
	}, nil
}

func (e *Engine) titles(ctx context.Context, p domain.Principal) (map[string]string, error) {
	buckets, err := e.docBuckets(ctx, p)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(buckets))
	for _, b := range buckets {
		out[b.Key] = b.title()
	}
	return out, nil
}

type docBucket struct {
	Key      string `json:"key"`
	DocCount int    `json:"doc_count"`
	Meta     struct {
		Hits struct {
			Hits []struct {
				Source struct {
					Source string `json:"source"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	} `json:"meta"`
}

func (b docBucket) title() string {
	if len(b.Meta.Hits.Hits) == 0 {
		return ""
	}
	return b.Meta.Hits.Hits[0].Source.Source
}

func (e *Engine) docBuckets(ctx context.Context, p domain.Principal) ([]docBucket, error) {
	body := map[string]any{
		"size":  0,
		"query": e.chunkFilter(p, ""),
		"aggs": map[string]any{
			"docs": map[string]any{
				"terms": map[string]any{"field": "doc_id", "size": maxListedDocs},
				"aggs": map[string]any{
					"meta": map[string]any{"top_hits": map[string]any{"size": 1, "_source": []string{"source"}}},
				},
			},
		},
	}
	var res struct {
		Aggregations struct {
			Docs struct {
				Buckets []docBucket `json:"buckets"`
			} `json:"docs"`
		} `json:"aggregations"`
	}
	if err := e.esSearch(ctx, body, &res); err != nil {
		return nil, err
	}
	return res.Aggregations.Docs.Buckets, nil
}

func (e *Engine) chunkFilter(p domain.Principal, docID string) map[string]any {
	filter := []any{
		map[string]any{"term": map[string]any{store.TenantField: p.Tenant}},
		map[string]any{"term": map[string]any{"quarantined": false}},
	}
	if docID != "" {
		filter = append(filter, map[string]any{"term": map[string]any{"doc_id": docID}})
	}
	return map[string]any{"bool": map[string]any{"filter": filter}}
}

func (e *Engine) esSearch(ctx context.Context, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode elasticsearch query: %w", err)
	}
	req := store.RawRequest(http.MethodPost, "/"+store.IndexChunks+"/_search", raw, "application/json")
	status, resp, err := e.es.Do(ctx, req)
	if err != nil {
		return fmt.Errorf("%w: %w", err, domain.ErrUnavailable)
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("elasticsearch returned %d: %w", status, domain.ErrUnavailable)
	}
	if err := json.Unmarshal(resp, out); err != nil {
		return fmt.Errorf("decode elasticsearch response: %w", err)
	}
	return nil
}
