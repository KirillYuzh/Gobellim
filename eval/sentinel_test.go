package eval

import (
	"errors"
	"testing"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

func TestRecordedWithoutATraceReturnsErrNotFound(t *testing.T) {
	rec := NewRecorder()
	_, err := Recorded(rec)
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, err := Recorded(nil); !errors.Is(err, core.ErrInvalidConfig) {
		t.Fatalf("Recorded(nil) err = %v, want ErrInvalidConfig", err)
	}
}

func TestRecordedWithATraceDoesNotReturnNotFound(t *testing.T) {
	rec := NewRecorder()
	rec.Enqueue(TraceRecord{ID: "t1", Name: "generate", Output: `{"approved":true}`})
	if _, err := Recorded(rec); errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v, must not be ErrNotFound", err)
	}
}
