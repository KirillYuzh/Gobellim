package store

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrBulkItem is the sentinel wrapped by bulk item failures, including
// VersionConflictError.
var ErrBulkItem = errors.New("bulk item failed")

// VersionConflictError reports a per-item _bulk version conflict (HTTP 409 or
// version_conflict_engine_exception).
type VersionConflictError struct {
	Action string
	Status int
	Type   string
	Reason string
}

// Error returns the conflicting bulk action, status, error type, and reason.
func (e *VersionConflictError) Error() string {
	return fmt.Sprintf("store: bulk %s version conflict with status %d: %s: %s",
		e.Action, e.Status, e.Type, e.Reason)
}

// Unwrap returns ErrBulkItem so errors.Is matches bulk item failures.
func (e *VersionConflictError) Unwrap() error { return ErrBulkItem }

// IsVersionConflict reports whether err is or wraps a VersionConflictError.
func IsVersionConflict(err error) bool {
	var vc *VersionConflictError
	return errors.As(err, &vc)
}

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

const versionConflictType = "version_conflict_engine_exception"

// BulkErrors inspects a _bulk response, returning nil when no item failed, a
// VersionConflictError for a 409 or version_conflict_engine_exception item, and
// otherwise an error wrapping ErrBulkItem. An empty body is treated as success.
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
			if result.Status == 409 || result.Error.Type == versionConflictType {
				return &VersionConflictError{
					Action: action,
					Status: result.Status,
					Type:   result.Error.Type,
					Reason: result.Error.Reason,
				}
			}
			return fmt.Errorf("store: bulk %s failed with status %d: %s: %s: %w",
				action, result.Status, result.Error.Type, result.Error.Reason, ErrBulkItem)
		}
	}
	return fmt.Errorf("store: bulk reported errors: %w", ErrBulkItem)
}
