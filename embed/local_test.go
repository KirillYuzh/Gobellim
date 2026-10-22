//go:build !ort

package embed

import (
	"errors"
	"testing"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

func TestNewLocalUnavailableWithoutORT(t *testing.T) {
	if _, err := NewLocal(LocalConfig{Dir: "./models", Model: "x"}); err == nil {
		t.Fatal("NewLocal must fail in the default build")
	}
}

func TestVerifyDirUsesTheSharedPresetRule(t *testing.T) {
	root := t.TempDir()
	const known = "intfloat/multilingual-e5-small"
	const unknown = "unknown/model"
	writeModelDir(t, root, known)
	writeModelDir(t, root, unknown)

	if err := VerifyDir(root, known); err != nil {
		t.Fatalf("VerifyDir(%q): %v", known, err)
	}
	if err := VerifyDir(root, unknown); !errors.Is(err, core.ErrInvalidConfig) {
		t.Fatalf("VerifyDir(%q) err = %v, want %v", unknown, err, core.ErrInvalidConfig)
	}
}
