package store

import (
	"encoding/json"
	"errors"
	"fmt"
)

var ErrBulkItem = errors.New("bulk item failed")

type bulkError struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
}

type bulkItemResult struct {
	Status int        `json:"status"`
	Error  *bulkError `json:"error"`
}

type bulkResponse struct {
	Errors bool                        `json:"errors"`
	Items  []map[string]bulkItemResult `json:"items"`
}

// BulkErrors reports the first failed item of a _bulk response.
// Elasticsearch answers 200 even when individual documents fail,
// so a caller that only checks the status code loses writes silently.
func BulkErrors(body []byte) error {
	if len(body) == 0 {
		return nil
	}
	var res bulkResponse
	if err := json.Unmarshal(body, &res); err != nil {
		return fmt.Errorf("store: decode bulk response: %w", err)
	}
	if !res.Errors {
		return nil
	}
	for _, item := range res.Items {
		for action, result := range item {
			if result.Error == nil {
				continue
			}
			return fmt.Errorf("store: bulk %s failed with status %d: %s: %s: %w",
				action, result.Status, result.Error.Type, result.Error.Reason, ErrBulkItem)
		}
	}
	return fmt.Errorf("store: bulk reported errors: %w", ErrBulkItem)
}
