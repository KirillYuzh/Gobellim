package kit

import (
	"sort"
	"strings"
	"unicode"

	"github.com/gobellim/kit/model"
)

// DefaultToolSearchThreshold is the allowed-tool count above which tool search
// kicks in.
const DefaultToolSearchThreshold = 20

// DefaultToolSearchTopK is how many tools tool search exposes when TopK is 0.
const DefaultToolSearchTopK = 8

// ToolSearch narrows the tool set exposed to the model when a role may use many
// tools. Only tools the principal already holds are considered, so search can
// never widen access — it only hides tools the model would otherwise have to
// read through.
//
// Ranking is lexical and deterministic (no embedding call, no ML), which keeps
// it cheap and reproducible. When nothing matches the user's message the full
// set is exposed, so an uninformative query never hides a tool by accident.
type ToolSearch struct {
	Enabled   bool
	Threshold int // 0 → DefaultToolSearchThreshold
	TopK      int // 0 → DefaultToolSearchTopK
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

// rankTools returns the topK best-matching tools in best-first order. It
// returns specs unchanged when no tool matches the query.
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

// searchTokens splits a string into lowercase word tokens of two or more runes,
// deduplicated. It is unicode-aware, so Cyrillic queries work too.
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
