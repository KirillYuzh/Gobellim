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
	if !strings.Contains(err.Error(), "dimensions") {
		t.Fatalf("reason lost: %v", err)
	}
}
