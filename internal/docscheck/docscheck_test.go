// Package docscheck keeps the documentation from naming an API the code no
// longer has.
//
// Twice this repository shipped pages that described Access while the code said
// Operation, and both times every test passed: nothing imports a README. A
// rename that misses a page is only visible to a reader, so it is checked here
// instead.
package docscheck

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// retired lists the identifiers a rename removed. A page that still mentions
// one is describing another version of the library.
var retired = []string{
	"AccessRead", "AccessWrite", "AccessSend", "AccessDelete", "AccessCompute", "AccessUnknown",
	"EffectRead", "EffectWrite", "EffectEgress", "EffectDestructive", "EffectUnknown",
	"gobellim.Access", "gobellim.Effect",
	"Validated",
	"flow.NewSealer", "ApprovePending", "Suspension.Token",
}

// allow is the escape hatch for a page that discusses a retired name on
// purpose, such as a migration note: put this marker on the same line.
const allow = "docscheck: allow"

func TestDocsDoNotNameRetiredAPI(t *testing.T) {
	root := filepath.Join("..", "..")
	files := make([]string, 0, 16)
	for _, name := range []string{
		"README.md", "README.ru.md", "AGENTS.md", "DESIGN.md",
		"SECURITY.md", "SECURITY.ru.md",
	} {
		files = append(files, filepath.Join(root, name))
	}
	// A new page under docs/ is covered without touching this test.
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".md") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk docs: %v", err)
	}

	for _, path := range files {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, allow) {
				continue
			}
			for _, bad := range retired {
				if strings.Contains(line, bad) {
					t.Errorf("%s:%d mentions %q; the code no longer has it", rel, i+1, bad)
				}
			}
		}
	}
}
