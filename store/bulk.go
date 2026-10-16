package store

import (
	"encoding/json"
	"errors"
	"fmt"
)

var ErrBulkItem = errors.New("bulk item failed")

type VersionConflictError struct {
	Action string
	Status int
	Type   string
	Reason string
}

func (e *VersionConflictError) Error() string {
	return fmt.Sprintf("store: bulk %s version conflict with status %d: %s: %s",
		e.Action, e.Status, e.Type, e.Reason)
}

func (e *VersionConflictError) Unwrap() error { return ErrBulkItem }

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
