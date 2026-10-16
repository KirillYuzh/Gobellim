package eval

import (
	"errors"
	"testing"

	"github.com/KirillYuzh/Gobellim/internal/core"
	"github.com/KirillYuzh/Gobellim/trace"
)

func TestRecordedWithoutATraceReturnsErrNotFound(t *testing.T) {
	rec := trace.NewRecorder()
	_, err := Recorded(rec)
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, err := Recorded(nil); !errors.Is(err, core.ErrInvalidConfig) {
		t.Fatalf("Recorded(nil) err = %v, want ErrInvalidConfig", err)
	}
}

func TestRecordedWithATraceDoesNotReturnNotFound(t *testing.T) {
	rec := trace.NewRecorder()
	rec.Enqueue(trace.Trace{ID: "t1", Name: "generate", Output: `{"approved":true}`})
	if _, err := Recorded(rec); errors.Is(err, core.ErrNotFound) {
		t.Fatalf("err = %v, must not be ErrNotFound", err)
	}
}
