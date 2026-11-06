package gobellim

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// tagInDocs finds a struct tag as the documentation writes it: inside backticks,
// which is both how a Go file spells it and how every page quotes it.
var tagInDocs = regexp.MustCompile("gobellim:\"([^\"]*)\"")

// TestDocumentedTagsParse checks every `gobellim:"..."` the docs and the
// notebooks show against the parser that reads it.
//
// The tag subset is a contract: a reader copies a line from README into a
// struct and expects NewTask to accept it. A tag that only looks plausible --
// `untrusted`, a renamed `pii` -- is worse than a typo, because it reads as a
// protection. Nothing imports a page, so the check lives next to parseTag.
func TestDocumentedTagsParse(t *testing.T) {
	root := "."
	var files []string
	for _, name := range []string{
		"README.md", "README.ru.md", "AGENTS.md", "DESIGN.md",
		"SECURITY.md", "SECURITY.ru.md",
	} {
		files = append(files, filepath.Join(root, name))
	}
	for _, dir := range []string{"docs", "examples"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && (strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".go")) {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}

	checked := 0
	for _, path := range files {
		// The path is a fixed repository file or a file found under a fixed
		// directory; no caller supplies it.
		b, err := os.ReadFile(path) //nolint:gosec // G304: fixed repository paths
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			for _, m := range tagInDocs.FindAllStringSubmatch(line, -1) {
				raw := m[1]
				if raw == "..." {
					continue // the prose placeholder in AGENTS.md
				}
				// A Markdown table cell escapes the pipe inside oneof=a|b.
				raw = strings.ReplaceAll(raw, `\|`, "|")
				checked++
				if _, err := parseTag(raw); err != nil {
					t.Errorf("%s:%d: %q is not a tag NewTask accepts: %v", path, i+1, m[1], err)
				}
			}
		}
	}
	if checked < 20 {
		t.Fatalf("checked %d tags; the scan is broken, not the docs", checked)
	}
}
