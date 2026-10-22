package tool

import (
	"testing"
)

func TestCanonicalArgs(t *testing.T) {
	t.Parallel()

	t.Run("empty args", func(t *testing.T) {
		t.Parallel()
		if got := CanonicalArgs(nil); got != "{}" {
			t.Errorf("CanonicalArgs(nil) = %q, want %q", got, "{}")
		}
	})

	t.Run("sorted keys", func(t *testing.T) {
		t.Parallel()
		args := []byte(`{"z":1,"a":2,"m":3}`)
		got := CanonicalArgs(args)
		want := `{"a":2,"m":3,"z":1}`
		if got != want {
			t.Errorf("CanonicalArgs = %q, want %q", got, want)
		}
	})

	t.Run("invalid JSON returns as-is", func(t *testing.T) {
		t.Parallel()
		args := []byte(`{invalid}`)
		got := CanonicalArgs(args)
		if got != string(args) {
			t.Errorf("CanonicalArgs(%q) = %q, want same", args, got)
		}
	})
}
