// Package importrules enforces the dependency direction of the module with a
// stdlib-only test, so a cycle is caught before it reaches CI.
package importrules

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const module = "github.com/KirillYuzh/Gobellim"

func TestRootDoesNotImportOptionalPackages(t *testing.T) {
	root := filepath.Join("..", "..")
	optional := []string{"/flow", "/testkit", "/store/sql", "/plugins", "/examples"}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, e.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			for _, suffix := range optional {
				if strings.HasPrefix(path, module+suffix) {
					t.Errorf("%s imports optional package %s", e.Name(), path)
				}
			}
		}
	}
}

func TestFlowDoesNotImportTestkit(t *testing.T) {
	dir := filepath.Join("..", "..", "flow")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, e.Name()), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if strings.HasPrefix(path, module+"/testkit") {
				t.Errorf("flow/%s imports testkit", e.Name())
			}
		}
	}
}
