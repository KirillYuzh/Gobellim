package embed

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

// writeModelDir creates the directory a local model lives in and fills it with
// the files a local encoder needs.
func writeModelDir(t *testing.T, root, model string) string {
	t.Helper()
	dir := ModelDir(root, model)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, name := range localModelFiles {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestResolvePresetFromDirUsesFileConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := `{"pooling":"cls","query_prefix":"q: ","max_len":256}`
	if err := os.WriteFile(filepath.Join(dir, "kit_embedder.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := resolvePresetFromDir(dir, "intfloat/multilingual-e5-small")
	if err != nil {
		t.Fatal(err)
	}
	if p.Pooling != PoolingCLS || p.MaxLen != 256 || p.QueryPrefix != "q: " {
		t.Fatalf("preset = %+v", p)
	}
}

func TestResolvePresetFromDirFallsBackToModelFamily(t *testing.T) {
	dir := t.TempDir()
	p, err := resolvePresetFromDir(dir, "intfloat/multilingual-e5-small")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "e5" {
		t.Fatalf("preset = %+v, want the e5 preset", p)
	}
}

func TestResolvePresetFromDirRejectsUnknownModel(t *testing.T) {
	dir := t.TempDir()
	_, err := resolvePresetFromDir(dir, "unknown/model")
	if !errors.Is(err, core.ErrInvalidConfig) {
		t.Fatalf("err = %v, want %v", err, core.ErrInvalidConfig)
	}
	if !strings.Contains(err.Error(), "kit_embedder.json") {
		t.Fatalf("err = %v, want it to name kit_embedder.json", err)
	}
}

func TestResolvePresetFromDirRejectsAmbiguousModel(t *testing.T) {
	dir := t.TempDir()
	_, err := resolvePresetFromDir(dir, "my-bge-m3-tuned")
	if !errors.Is(err, core.ErrInvalidConfig) {
		t.Fatalf("err = %v, want %v", err, core.ErrInvalidConfig)
	}
}

func TestVerifyDirSharesThePresetRule(t *testing.T) {
	root := t.TempDir()
	const known = "intfloat/multilingual-e5-small"
	const unknown = "unknown/model"
	writeModelDir(t, root, known)
	writeModelDir(t, root, unknown)

	if _, err := resolvePresetFromDir(ModelDir(root, known), known); err != nil {
		t.Fatalf("resolvePresetFromDir: %v", err)
	}
	if err := verifyDir(root, known); err != nil {
		t.Fatalf("verifyDir(%q): %v", known, err)
	}

	if _, err := resolvePresetFromDir(ModelDir(root, unknown), unknown); !errors.Is(err, core.ErrInvalidConfig) {
		t.Fatalf("resolvePresetFromDir err = %v, want %v", err, core.ErrInvalidConfig)
	}
	if err := verifyDir(root, unknown); !errors.Is(err, core.ErrInvalidConfig) {
		t.Fatalf("verifyDir(%q) err = %v, want %v", unknown, err, core.ErrInvalidConfig)
	}
}
