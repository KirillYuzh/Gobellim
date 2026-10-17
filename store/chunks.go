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

// Document returns the chunk's Elasticsearch source map. Quarantined chunks
// omit text, vector, and embed_model, and CreatedAt defaults to now when zero.
func (c Chunk) Document() map[string]any {
	doc := map[string]any{
		"doc_id":      c.DocID,
		TenantField:   c.Tenant,
		ACLField:      c.ACL,
		"position":    c.Position,
		"quarantined": c.Quarantine,
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

// ValidateChunk rejects a quarantined chunk that carries text or a vector, and a
// non-quarantined chunk without a vector or with a vector length that differs
// from dims when dims > 0. Errors wrap core.ErrInvalidArgs.
func ValidateChunk(c Chunk, dims int) error {
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

// ChunksBulkBody validates every chunk and encodes them into a _bulk body for
// IndexChunks.
func ChunksBulkBody(chunks []Chunk, dims int) ([]byte, error) {
	docs := make([]Doc, 0, len(chunks))
	for _, c := range chunks {
		if err := ValidateChunk(c, dims); err != nil {
			return nil, err
		}
		docs = append(docs, Doc{ID: c.ID, Source: c.Document()})
	}
	return BulkBody(IndexChunks, docs)
}
