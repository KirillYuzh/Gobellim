package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

// Doc is one document in a bulk request: ID is written as _id when non-empty,
// and Source is marshalled as the document body.
type Doc struct {
	ID     string
	Source any
}

// BulkBody returns a newline-delimited _bulk body using the index action for
// each doc.
func BulkBody(index string, docs []Doc) ([]byte, error) {
	return bulkBody("index", index, docs)
}

// BulkCreateBody returns a newline-delimited _bulk body using the create action
// for each doc.
func BulkCreateBody(index string, docs []Doc) ([]byte, error) {
	return bulkBody("create", index, docs)
}

func bulkBody(op, index string, docs []Doc) ([]byte, error) {
	var buf bytes.Buffer
	for _, d := range docs {
		meta := map[string]any{"_index": index}
		if d.ID != "" {
			meta["_id"] = d.ID
		}
		action, err := json.Marshal(map[string]any{op: meta})
		if err != nil {
			return nil, fmt.Errorf("store: encode bulk action: %w", err)
		}
		src, err := json.Marshal(d.Source)
		if err != nil {
			return nil, fmt.Errorf("store: encode bulk document: %w", err)
		}
		buf.Write(action)
		buf.WriteByte('\n')
		buf.Write(src)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

// Chunk is one indexed piece of a source document, carrying tenant, ACL, text,
// vector, and document version.
type Chunk struct {
	ID         string
	DocID      string
	Tenant     string
	ACL        []string
	Text       string
	Vector     []float32
	EmbedModel string
	DocVersion int64
	Position   int
	Source     string
	CreatedAt  time.Time
	Quarantine bool
}

// GetID returns the chunk identifier.
func (c Chunk) GetID() string { return c.ID }

// Document returns the chunk's Elasticsearch source map. Quarantined chunks
// omit text, vector, and embed_model, and CreatedAt defaults to now when zero.
// A nil ACL is written as an empty list so the field is never null.
func (c Chunk) Document() map[string]any {
	acl := c.ACL
	if acl == nil {
		acl = []string{}
	}
	doc := map[string]any{
		"doc_id":        c.DocID,
		TenantField:     c.Tenant,
		ACLField:        acl,
		"position":      c.Position,
		QuarantineField: c.Quarantine,
	}
	if !c.Quarantine {
		doc[TextField] = c.Text
		if len(c.Vector) > 0 {
			doc[VectorField] = c.Vector
		}
		if c.EmbedModel != "" {
			doc[EmbedModelField] = c.EmbedModel
		}
	}
	if c.Source != "" {
		doc["source"] = c.Source
	}
	if c.DocVersion > 0 {
		doc["doc_version"] = c.DocVersion
	}
	created := c.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	doc["created_at"] = created.UTC().Format(time.RFC3339Nano)
	return doc
}

// ValidateChunk requires the chunk identity (ID, DocID, Tenant and a non-empty
// ACL), rejects a quarantined chunk that carries text or a vector, and rejects
// a non-quarantined chunk without a vector or with a vector length that differs
// from dims when dims > 0. Errors wrap core.ErrInvalidArgs.
func ValidateChunk(c Chunk, dims int) error {
	if c.ID == "" {
		return fmt.Errorf("store: chunk has no id: %w", core.ErrInvalidArgs)
	}
	if c.DocID == "" {
		return fmt.Errorf("store: chunk %q has no doc id: %w", c.ID, core.ErrInvalidArgs)
	}
	if c.Tenant == "" {
		return fmt.Errorf("store: chunk %q has no tenant: %w", c.ID, core.ErrInvalidArgs)
	}
	if len(c.ACL) == 0 {
		return fmt.Errorf("store: chunk %q has no acl: %w", c.ID, core.ErrInvalidArgs)
	}
	if c.Quarantine {
		if c.Text != "" {
			return fmt.Errorf("store: quarantined chunk %q must not carry text: %w", c.ID, core.ErrInvalidArgs)
		}
		if len(c.Vector) > 0 {
			return fmt.Errorf("store: quarantined chunk %q must not carry a vector: %w", c.ID, core.ErrInvalidArgs)
		}
		return nil
	}
	if len(c.Vector) == 0 {
		return fmt.Errorf("store: chunk %q has no vector: %w", c.ID, core.ErrInvalidArgs)
	}
	if dims > 0 && len(c.Vector) != dims {
		return fmt.Errorf("store: chunk %q vector has %d dims, want %d: %w", c.ID, len(c.Vector), dims, core.ErrInvalidArgs)
	}
	return nil
}

// validateAndBuildDocs validates items and builds Doc slice for bulk requests.
func validateAndBuildDocs[V any](items []V, validate func(V, int) error, dims int, docFunc func(V) map[string]any, idFunc func(V) string) ([]Doc, error) {
	docs := make([]Doc, 0, len(items))
	for _, item := range items {
		if err := validate(item, dims); err != nil {
			return nil, err
		}
		docs = append(docs, Doc{ID: idFunc(item), Source: docFunc(item)})
	}
	return docs, nil
}

// ChunksBulkBody validates every chunk and encodes them into a _bulk body for
// IndexChunks.
func ChunksBulkBody(chunks []Chunk, dims int) ([]byte, error) {
	docs, err := validateAndBuildDocs(chunks, ValidateChunk, dims, func(c Chunk) map[string]any { return c.Document() }, func(c Chunk) string { return c.ID })
	if err != nil {
		return nil, err
	}
	return BulkBody(IndexChunks, docs)
}
