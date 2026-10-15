package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

type Doc struct {
	ID     string
	Source any
}

func BulkBody(index string, docs []Doc) ([]byte, error) {
	return bulkBody("index", index, docs)
}

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

// Chunk is one indexed passage; the ACL is computed at write time and stored
// in the document.
type Chunk struct {
	ID         string
	DocID      string
	Tenant     string
	ACL        []string
	Text       string
	Vector     []float32
	Position   int
	Source     string
	CreatedAt  time.Time
	Quarantine bool
}

func (c Chunk) Document() map[string]any {
	doc := map[string]any{
		"doc_id":      c.DocID,
		TenantField:   c.Tenant,
		ACLField:      c.ACL,
		TextField:     c.Text,
		"position":    c.Position,
		"quarantined": c.Quarantine,
	}
	if len(c.Vector) > 0 {
		doc[VectorField] = c.Vector
	}
	if c.Source != "" {
		doc["source"] = c.Source
	}
	created := c.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	doc["created_at"] = created.UTC().Format(time.RFC3339Nano)
	return doc
}

func ChunksBulkBody(chunks []Chunk) ([]byte, error) {
	docs := make([]Doc, 0, len(chunks))
	for _, c := range chunks {
		docs = append(docs, Doc{ID: c.ID, Source: c.Document()})
	}
	return BulkBody(IndexChunks, docs)
}
