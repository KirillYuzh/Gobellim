package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

// MemoryMessage is one stored thread message, keyed by tenant and user and
// optionally carrying an embedding.
type MemoryMessage struct {
	ID         string
	ThreadID   string
	Tenant     string
	UserID     string
	Role       string
	Content    string
	Vector     []float32
	EmbedModel string
	Seq        int64
	Untrusted  bool
	CreatedAt  time.Time
}

// Document returns the message's Elasticsearch source map, defaulting CreatedAt
// to now when zero and adding vector and embed_model only when set.
func (m MemoryMessage) Document() map[string]any {
	created := m.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	doc := map[string]any{
		ThreadField:    m.ThreadID,
		TenantField:    m.Tenant,
		UserIDField:    m.UserID,
		RoleField:      m.Role,
		ContentField:   m.Content,
		"seq":          m.Seq,
		UntrustedField: m.Untrusted,
		"created_at":   created.UTC().Format(time.RFC3339Nano),
	}
	if len(m.Vector) > 0 {
		doc[VectorField] = m.Vector
	}
	if m.EmbedModel != "" {
		doc[EmbedModelField] = m.EmbedModel
	}
	return doc
}

// ValidateMemoryMessage requires a non-empty thread_id, tenant, and content and
// a positive seq; a non-empty vector must match dims when dims > 0, and errors
// wrap core.ErrInvalidArgs.
func ValidateMemoryMessage(m MemoryMessage, dims int) error {
	if m.ThreadID == "" {
		return fmt.Errorf("store: memory message has no thread_id: %w", core.ErrInvalidArgs)
	}
	if m.Tenant == "" {
		return fmt.Errorf("store: memory message has no tenant: %w", core.ErrInvalidArgs)
	}
	if m.Content == "" {
		return fmt.Errorf("store: memory message has empty content: %w", core.ErrInvalidArgs)
	}
	if m.Seq <= 0 {
		return fmt.Errorf("store: memory message %q has no sequence: %w", m.ThreadID, core.ErrInvalidArgs)
	}
	if dims > 0 && len(m.Vector) > 0 && len(m.Vector) != dims {
		return fmt.Errorf("store: memory message vector has %d dims, want %d: %w", len(m.Vector), dims, core.ErrInvalidArgs)
	}
	return nil
}

// MemoryBulkBody validates every message and encodes them into a _bulk body for
// IndexMemoryMessages.
func MemoryBulkBody(msgs []MemoryMessage, dims int) ([]byte, error) {
	docs := make([]Doc, 0, len(msgs))
	for _, m := range msgs {
		if err := ValidateMemoryMessage(m, dims); err != nil {
			return nil, err
		}
		docs = append(docs, Doc{ID: m.ID, Source: m.Document()})
	}
	return BulkBody(IndexMemoryMessages, docs)
}

// MemoryHistoryBody returns a search body for the newest limit messages of a
// tenant, user, and thread, sorted by seq descending; it returns nil when
// limit <= 0.
func MemoryHistoryBody(tenant, userID, threadID string, limit int) []byte {
	if limit <= 0 {
		return nil
	}
	body := map[string]any{
		"size": limit,
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []any{
					map[string]any{"term": map[string]any{TenantField: tenant}},
					map[string]any{"term": map[string]any{UserIDField: userID}},
					map[string]any{"term": map[string]any{ThreadField: threadID}},
				},
			},
		},
		"sort":    []any{map[string]any{"seq": map[string]any{"order": "desc"}}},
		"_source": []string{RoleField, ContentField, UntrustedField, "created_at"},
	}
	raw, _ := json.Marshal(body)
	return raw
}

// MemoryRecallBody returns a kNN search body over all threads of a tenant and
// user, defaulting and clamping TopK and numCandidates like search. A missing
// tenant or vector wraps core.ErrInvalidArgs.
func MemoryRecallBody(tenant, userID string, vector []float32, topK, numCandidates int) ([]byte, error) {
	if tenant == "" {
		return nil, fmt.Errorf("store: memory recall requires a tenant: %w", core.ErrInvalidArgs)
	}
	if len(vector) == 0 {
		return nil, fmt.Errorf("store: memory recall requires a query vector: %w", core.ErrInvalidArgs)
	}
	if topK <= 0 {
		topK = DefaultTopK
	}
	if topK > MaxTopK {
		topK = MaxTopK
	}
	if numCandidates <= 0 {
		numCandidates = topK * 10
		if numCandidates < 100 {
			numCandidates = 100
		}
	}
	if numCandidates > MaxNumCandidates {
		numCandidates = MaxNumCandidates
	}
	filter := map[string]any{
		"bool": map[string]any{
			"filter": []any{
				map[string]any{"term": map[string]any{TenantField: tenant}},
				map[string]any{"term": map[string]any{UserIDField: userID}},
			},
		},
	}
	body := map[string]any{
		"size": topK,
		"query": map[string]any{
			"knn": map[string]any{
				"field":          VectorField,
				"query_vector":   vector,
				"num_candidates": numCandidates,
				"filter":         filter,
			},
		},
		"_source": []string{RoleField, ContentField, UntrustedField, "created_at"},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("store: encode memory recall: %w", err)
	}
	return raw, nil
}

// MemoryClearBody returns a delete-by-query body matching the given tenant,
// user, and thread.
func MemoryClearBody(tenant, userID, threadID string) []byte {
	body := map[string]any{
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []any{
					map[string]any{"term": map[string]any{TenantField: tenant}},
					map[string]any{"term": map[string]any{UserIDField: userID}},
					map[string]any{"term": map[string]any{ThreadField: threadID}},
				},
			},
		},
	}
	raw, _ := json.Marshal(body)
	return raw
}

// WorkingMemory is the single structured working-memory document stored per
// thread.
type WorkingMemory struct {
	ThreadID  string
	Tenant    string
	UserID    string
	Data      map[string]any
	UpdatedAt time.Time
}

// Document returns the working-memory source map, defaulting UpdatedAt to now
// when zero and Data to an empty object when nil.
func (w WorkingMemory) Document() map[string]any {
	updated := w.UpdatedAt
	if updated.IsZero() {
		updated = time.Now().UTC()
	}
	data := w.Data
	if data == nil {
		data = map[string]any{}
	}
	return map[string]any{
		ThreadField:  w.ThreadID,
		TenantField:  w.Tenant,
		UserIDField:  w.UserID,
		"data":       data,
		"updated_at": updated.UTC().Format(time.RFC3339Nano),
	}
}

// MemoryWorkingID returns the document ID for a thread's working memory: the hex
// SHA-256 of tenant, a NUL byte, and threadID.
func MemoryWorkingID(tenant, threadID string) string {
	h := sha256.New()
	h.Write([]byte(tenant))
	h.Write([]byte{0})
	h.Write([]byte(threadID))
	return hex.EncodeToString(h.Sum(nil))
}

// MemoryHit is one recalled memory message with its relevance score.
type MemoryHit struct {
	Role      string
	Content   string
	Untrusted bool
	CreatedAt time.Time
	Score     float64
}

// ParseMemorySearch decodes a search response into memory hits, parsing
// created_at as RFC3339Nano. An error field wraps core.ErrProvider, and an
// unparsable timestamp is returned as an error.
func ParseMemorySearch(body []byte) ([]MemoryHit, error) {
	var parsed struct {
		Error json.RawMessage `json:"error"`
		Hits  struct {
			Hits []struct {
				Score  float64 `json:"_score"`
				Source struct {
					Role      string `json:"role"`
					Content   string `json:"content"`
					Untrusted bool   `json:"untrusted"`
					CreatedAt string `json:"created_at"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("store: parse memory response: %w", err)
	}
	if len(parsed.Error) > 0 {
		return nil, fmt.Errorf("store: memory search failed: %s: %w", Snippet(parsed.Error), core.ErrProvider)
	}
	out := make([]MemoryHit, 0, len(parsed.Hits.Hits))
	for _, h := range parsed.Hits.Hits {
		created := time.Time{}
		if h.Source.CreatedAt != "" {
			parsed, err := time.Parse(time.RFC3339Nano, h.Source.CreatedAt)
			if err != nil {
				return nil, fmt.Errorf("store: memory created_at %q: %w", h.Source.CreatedAt, err)
			}
			created = parsed
		}
		out = append(out, MemoryHit{
			Role:      h.Source.Role,
			Content:   h.Source.Content,
			Untrusted: h.Source.Untrusted,
			CreatedAt: created,
			Score:     h.Score,
		})
	}
	return out, nil
}
