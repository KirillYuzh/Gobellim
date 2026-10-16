//go:build ort

package gliner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFixturesParity(t *testing.T) {
	dir := os.Getenv("KIT_MODELS_DIR")
	if dir == "" {
		dir = filepath.Join("..", "..", "models")
	}
	if _, err := os.Stat(filepath.Join(dir, DefaultModel, "model.onnx")); err != nil {
		t.Skipf("model %s is not present under %s", DefaultModel, dir)
	}
	lib := os.Getenv("KIT_ORT_LIB")
	if lib == "" {
		lib = filepath.Join("..", "..", defaultLibPath())
	}
	if !filepath.IsAbs(lib) {
		abs, err := filepath.Abs(filepath.Join("..", "..", lib))
		if err != nil {
			t.Fatal(err)
		}
		lib = abs
	}
	if _, err := os.Stat(lib); err != nil {
		t.Skipf("ORT library is not present at %s", lib)
	}
	t.Setenv("KIT_ORT_LIB", lib)

	report, err := CheckParity(dir, "testdata", 1.0)
	if err != nil {
		t.Fatalf("parity: %v", err)
	}
	for _, f := range report.Failures {
		t.Errorf("FAIL %s", f)
	}
	t.Logf("%d/%d fixtures passed", report.Agreed, report.Compared)
}
