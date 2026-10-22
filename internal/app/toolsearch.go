package app

import (
	"sort"
	"strings"
	"unicode"

	"github.com/KirillYuzh/Gobellim/model"
)

// DefaultToolSearchThreshold is the tool count above which tool search ranks the
// catalog by query relevance.
const DefaultToolSearchThreshold = 20

// DefaultToolSearchTopK is the number of tools kept by tool search when
// ToolSearch.TopK is zero.
const DefaultToolSearchTopK = 8

// ToolSearch configures lexical pre-filtering of a large tool catalog; a
// non-positive Threshold uses DefaultToolSearchThreshold and a non-positive TopK
// uses DefaultToolSearchTopK.
type ToolSearch struct {
	Enabled   bool
	Threshold int
	TopK      int
}

func (a *App) toolSearch(specs []model.ToolSpec, query string) []model.ToolSpec {
	if !a.cfg.ToolSearch.Enabled || len(specs) == 0 {
		return specs
	}
	threshold := a.cfg.ToolSearch.Threshold
	if threshold <= 0 {
		threshold = DefaultToolSearchThreshold
	}
	if len(specs) <= threshold {
		return specs
	}
	topK := a.cfg.ToolSearch.TopK
	if topK <= 0 {
		topK = DefaultToolSearchTopK
	}
	return rankTools(specs, query, topK)
}

func rankTools(specs []model.ToolSpec, query string, topK int) []model.ToolSpec {
	tokens := searchTokens(query)
	if len(tokens) == 0 {
		return specs
	}
	scores := make([]int, len(specs))
	best := 0
	for i, s := range specs {
		scores[i] = scoreTool(s, tokens)
		if scores[i] > best {
			best = scores[i]
		}
	}
	if best == 0 {
		return specs
	}

	indices := make([]int, len(specs))
	for i := range indices {
		indices[i] = i
	}
	sort.SliceStable(indices, func(i, j int) bool {
		a, b := indices[i], indices[j]
		if scores[a] != scores[b] {
			return scores[a] > scores[b]
		}
		return a < b
	})
	if topK > len(indices) {
		topK = len(indices)
	}
	out := make([]model.ToolSpec, 0, topK)
	for _, idx := range indices[:topK] {
		out = append(out, specs[idx])
	}
	return out
}

func scoreTool(s model.ToolSpec, tokens []string) int {
	name := strings.ToLower(s.Name)
	description := strings.ToLower(s.Description)
	score := 0
	for _, tok := range tokens {
		if strings.Contains(name, tok) {
			score += 3
		}
		if strings.Contains(description, tok) {
			score++
		}
	}
	return score
}

func searchTokens(s string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, 8)
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		tok := strings.ToLower(b.String())
		b.Reset()
		if len([]rune(tok)) < 2 {
			return
		}
		if _, ok := seen[tok]; ok {
			return
		}
		seen[tok] = struct{}{}
		out = append(out, tok)
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return out
}
