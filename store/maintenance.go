package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

// Snippet returns at most the first 200 bytes of b for a bounded error
// excerpt. The cut lands on a UTF-8 rune boundary and invalid bytes are
// dropped, so the excerpt is always valid UTF-8.
func Snippet(b []byte) string {
	const n = 200
	if len(b) > n {
		b = b[:n]
		for len(b) > 0 && !utf8.RuneStart(b[len(b)-1]) {
			b = b[:len(b)-1]
		}
	}
	return strings.ToValidUTF8(string(b), "")
}

// validateDocIDAndTenant validates that docID and tenant are non-empty.
func validateDocIDAndTenant(docID, tenant string) error {
	if strings.TrimSpace(docID) == "" {
		return fmt.Errorf("store: delete by doc requires a doc id: %w", core.ErrInvalidArgs)
	}
	if strings.TrimSpace(tenant) == "" {
		return fmt.Errorf("store: delete by doc requires a tenant: %w", core.ErrInvalidArgs)
	}
	return nil
}

// DeleteByDocBody returns a delete-by-query body matching the given doc_id and
// tenant; blank values wrap core.ErrInvalidArgs.
func DeleteByDocBody(docID, tenant string) ([]byte, error) {
	if err := validateDocIDAndTenant(docID, tenant); err != nil {
		return nil, err
	}
	body := map[string]any{
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []any{
					map[string]any{"term": map[string]any{"doc_id": docID}},
					map[string]any{"term": map[string]any{TenantField: tenant}},
				},
			},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("store: encode delete by doc body: %w", err)
	}
	return raw, nil
}

// DeleteByDocVersionBody returns a delete-by-query body matching doc_id and
// tenant while excluding chunks whose doc_version equals keepVersion.
func DeleteByDocVersionBody(docID, tenant string, keepVersion int64) ([]byte, error) {
	if err := validateDocIDAndTenant(docID, tenant); err != nil {
		return nil, err
	}
	body := map[string]any{
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []any{
					map[string]any{"term": map[string]any{"doc_id": docID}},
					map[string]any{"term": map[string]any{TenantField: tenant}},
				},
				"must_not": []any{
					map[string]any{"term": map[string]any{"doc_version": keepVersion}},
				},
			},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("store: encode delete by doc version body: %w", err)
	}
	return raw, nil
}

type deleteByQueryResponse struct {
	Deleted          int               `json:"deleted"`
	VersionConflicts int               `json:"version_conflicts"`
	Failures         []json.RawMessage `json:"failures"`
}

// ParseDeleteByQuery decodes a _delete_by_query response, returning the deleted
// count and an error wrapping core.ErrProvider when failures or version
// conflicts are reported.
func ParseDeleteByQuery(body []byte) (deleted int, err error) {
	var res deleteByQueryResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return 0, fmt.Errorf("store: parse delete by query response: %w", err)
	}
	if len(res.Failures) > 0 {
		return res.Deleted, fmt.Errorf("store: delete by query reported %d failures: %s: %w",
			len(res.Failures), Snippet(body), core.ErrProvider)
	}
	if res.VersionConflicts > 0 {
		return res.Deleted, fmt.Errorf("store: delete by query reported %d version conflicts: %w",
			res.VersionConflicts, core.ErrProvider)
	}
	return res.Deleted, nil
}

// PruneBody returns a delete-by-query body removing documents older than
// olderThan by @timestamp; a negative duration is treated as zero.
func PruneBody(olderThan time.Duration) ([]byte, error) {
	if olderThan < 0 {
		olderThan = 0
	}
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339Nano)
	body := map[string]any{
		"query": map[string]any{
			"range": map[string]any{
				"@timestamp": map[string]any{"lt": cutoff},
			},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("store: encode prune body: %w", err)
	}
	return raw, nil
}
