// Package repository — клиенты внешних систем.
package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"some-project/internal/entity"
)

const maxResponseBytes = 4 << 20

// Mockd — клиент mock-сервиса.
type Mockd struct {
	base string
	hc   *http.Client
}

// NewMockd создаёт клиент.
func NewMockd(base string) *Mockd {
	return &Mockd{base: base, hc: &http.Client{Timeout: 10 * time.Second}}
}

// Lookup — ответ upstream/lookup.
type Lookup struct {
	Code     string `json:"code"`
	Body     string `json:"body"`
	Question string `json:"question"`
	Error    string `json:"error"`
}

// Followup — ответ upstream/followup.
type Followup struct {
	Code  string `json:"code"`
	Body  string `json:"body"`
	Error string `json:"error"`
}

// KB возвращает чанки mock-базы знаний.
func (c *Mockd) KB(ctx context.Context, query string) ([]entity.Chunk, error) {
	var out struct {
		Chunks []entity.Chunk `json:"chunks"`
	}
	if err := c.post(ctx, "/kb/search", map[string]any{"query": query}, &out); err != nil {
		return nil, err
	}
	return out.Chunks, nil
}

// Lookup вызывает upstream/lookup.
func (c *Mockd) Lookup(ctx context.Context, query string) (Lookup, error) {
	var out Lookup
	if err := c.post(ctx, "/upstream/lookup", map[string]any{"query": query}, &out); err != nil {
		return Lookup{}, err
	}
	return out, nil
}

// Followup вызывает upstream/followup.
func (c *Mockd) Followup(ctx context.Context, query, input string) (Followup, error) {
	var out Followup
	if err := c.post(ctx, "/upstream/followup", map[string]any{"query": query, "input": input}, &out); err != nil {
		return Followup{}, err
	}
	return out, nil
}

func (c *Mockd) post(ctx context.Context, path string, in, out any) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("mockd: encode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("mockd: request: %w: %w", err, entity.ErrUpstream)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("mockd: %w: %w", err, entity.ErrUpstream)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("mockd: read: %w: %w", err, entity.ErrUpstream)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mockd: status %d: %w", resp.StatusCode, entity.ErrUpstream)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("mockd: decode: %w: %w", err, entity.ErrUpstream)
	}
	return nil
}
