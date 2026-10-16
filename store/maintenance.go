package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gobellim/kit/internal/core"
)

func DeleteByDocBody(docID, tenant string) ([]byte, error) {
	if strings.TrimSpace(docID) == "" {
		return nil, fmt.Errorf("store: delete by doc requires a doc id: %w", core.ErrInvalidArgs)
	}
	if strings.TrimSpace(tenant) == "" {
		return nil, fmt.Errorf("store: delete by doc requires a tenant: %w", core.ErrInvalidArgs)
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
	return json.Marshal(body)
}

type deleteByQueryResponse struct {
	Deleted          int               `json:"deleted"`
	VersionConflicts int               `json:"version_conflicts"`
	Failures         []json.RawMessage `json:"failures"`
}

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

func PruneBody(olderThan time.Duration) []byte {
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
	b, err := json.Marshal(body)
	if err != nil {
		panic(fmt.Sprintf("store: encode prune body: %v", err))
	}
	return b
}
