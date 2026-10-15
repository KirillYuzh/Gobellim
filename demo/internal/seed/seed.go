package seed

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/gobellim/kit/demo/internal/domain"
)

//go:embed kb/*.md
var files embed.FS

func Docs() ([]domain.KnowledgeDoc, error) {
	entries, err := fs.ReadDir(files, "kb")
	if err != nil {
		return nil, err
	}
	docs := make([]domain.KnowledgeDoc, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".md" {
			continue
		}
		raw, err := files.ReadFile("kb/" + e.Name())
		if err != nil {
			return nil, err
		}
		title, text := split(string(raw))
		if title == "" || text == "" {
			return nil, fmt.Errorf("seed: %s needs a '# title' line and a body", e.Name())
		}
		docs = append(docs, domain.KnowledgeDoc{
			ID:    strings.TrimSuffix(e.Name(), ".md"),
			Title: title,
			Text:  text,
			ACL:   []string{string(domain.RoleUser), string(domain.RoleAdmin)},
		})
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].ID < docs[j].ID })
	return docs, nil
}

func split(raw string) (title, text string) {
	body := strings.TrimSpace(raw)
	if !strings.HasPrefix(body, "# ") {
		return "", body
	}
	head, rest, found := strings.Cut(strings.TrimPrefix(body, "# "), "\n")
	if !found {
		return strings.TrimSpace(head), ""
	}
	return strings.TrimSpace(head), strings.TrimSpace(rest)
}
