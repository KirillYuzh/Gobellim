package store

import (
	"errors"
	"strings"
	"testing"
)

func TestBulkErrors(t *testing.T) {
	ok := []byte(`{"took":1,"errors":false,"items":[{"index":{"status":201}}]}`)
	if err := BulkErrors(ok); err != nil {
		t.Fatalf("clean response: %v", err)
	}
	if err := BulkErrors(nil); err != nil {
		t.Fatalf("empty response: %v", err)
	}

	bad := []byte(`{"took":1,"errors":true,"items":[{"index":{"status":400,"error":{"type":"illegal_argument_exception","reason":"The number of dimensions in the vector is incorrect"}}}]}`)
	err := BulkErrors(bad)
	if !errors.Is(err, ErrBulkItem) {
		t.Fatalf("want ErrBulkItem, got %v", err)
	}
	if IsVersionConflict(err) {
		t.Fatalf("400 must not be a version conflict: %v", err)
	}
	if !strings.Contains(err.Error(), "dimensions") {
		t.Fatalf("reason lost: %v", err)
	}
}

func TestBulkVersionConflict(t *testing.T) {
	body := []byte(`{"took":1,"errors":true,"items":[{"create":{"status":409,"error":{"type":"version_conflict_engine_exception","reason":"[1]: version conflict"}}}]}`)
	err := BulkErrors(body)
	if !IsVersionConflict(err) {
		t.Fatalf("want version conflict, got %v", err)
	}
	var vc *VersionConflictError
	if !errors.As(err, &vc) {
		t.Fatalf("want *VersionConflictError, got %T", err)
	}
	if vc.Action != "create" || vc.Status != 409 || vc.Type != versionConflictType {
		t.Fatalf("unexpected conflict: %+v", vc)
	}
	if !errors.Is(err, ErrBulkItem) {
		t.Fatalf("conflict must still match ErrBulkItem: %v", err)
	}

	byType := []byte(`{"errors":true,"items":[{"index":{"status":409,"error":{"type":"version_conflict_engine_exception","reason":"x"}}}]}`)
	if err := BulkErrors(byType); !IsVersionConflict(err) {
		t.Fatalf("type-only conflict missed: %v", err)
	}
}
