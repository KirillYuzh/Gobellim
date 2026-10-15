//go:build ort

package gliner

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

// The splitter and decoder below are pinned to the Python reference
// (hivetrace/gliner-guard-omni). See tools/gliner/SPEC.md before changing them.

const (
	pySpace = `\t\n\v\f\r \x{001c}-\x{001f}\x{0085}\x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}`
	pyWord  = `[\p{L}\p{N}_\p{M}\x{200c}\x{200d}]`

	// wordExpr reproduces Python's Unicode \w and \s semantics, including the
	// significant alternation order (leftmost, first alternative wins).
	wordExpr = `(?i)(?:https?://[^` + pySpace + `]+|www\.[^` + pySpace + `]+)` +
		`|[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}` +
		`|@[a-z0-9_]+` +
		`|` + pyWord + `+(?:[-_]` + pyWord + `+)*` +
		`|[^` + pySpace + `]`
)

type word struct {
	token string
	start int
	end   int
}

type span struct {
	text  string
	start int
	end   int
	score float64
}

type decodeResult struct {
	entities map[string][]span
	safety   string
	score    float64
}

func normalizeText(text string) string {
	runes := []rune(text)
	if len(runes) == 0 {
		return "."
	}
	switch runes[len(runes)-1] {
	case '.', '!', '?':
		return text
	}
	return text + "."
}

func (d *Detector) splitWords(text string) []word {
	matches := d.wordRe.FindAllStringIndex(text, -1)
	out := make([]word, 0, len(matches))
	for _, m := range matches {
		raw := text[m[0]:m[1]]
		start := utf8.RuneCountInString(text[:m[0]])
		out = append(out, word{
			token: strings.ToLower(raw),
			start: start,
			end:   start + utf8.RuneCountInString(raw),
		})
	}
	return out
}

func decode(cfg *modelConfig, p *prepared, probs, countLogits, clsLogits []float32) decodeResult {
	var res decodeResult
	labels := cfg.Schema.Entities.Labels
	cls := cfg.Schema.Classifications[0]
	if argmax(countLogits) >= 1 {
		n := len(p.words)
		res.entities = make(map[string][]span, len(labels))
		for qi, label := range labels {
			th := cfg.Decode.DefaultSpanThreshold
			if v, ok := cfg.Schema.Entities.Thresholds[label]; ok {
				th = v
			}
			var cands []span
			for start := 0; start < n; start++ {
				for width := 0; width < cfg.MaxWidth; width++ {
					pr := probs[(qi*n+start)*cfg.MaxWidth+width]
					end := start + width + 1
					if float64(pr) < th || end > n {
						continue
					}
					cs, ce := p.words[start].start, p.words[end-1].end
					surface := strings.TrimSpace(string(p.runes[cs:ce]))
					if surface == "" {
						continue
					}
					cands = append(cands, span{text: surface, start: cs, end: ce, score: float64(pr)})
				}
			}
			sort.SliceStable(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
			kept := make([]span, 0, len(cands))
			for _, c := range cands {
				overlaps := false
				for _, k := range kept {
					if c.start < k.end && k.start < c.end {
						overlaps = true
						break
					}
				}
				if !overlaps {
					kept = append(kept, c)
				}
			}
			seen := make(map[string]bool, len(kept))
			list := make([]span, 0, len(kept))
			for _, c := range kept {
				key := fmt.Sprintf("%s|%d|%d", strings.ToLower(c.text), c.start, c.end)
				if seen[key] {
					continue
				}
				seen[key] = true
				list = append(list, c)
			}
			res.entities[label] = list
		}
	}
	probsCls := softmax(clsLogits)
	best := 0
	for i, v := range probsCls {
		if v > probsCls[best] {
			best = i
		}
	}
	res.safety = cls.Labels[best]
	res.score = probsCls[best]
	return res
}

func softmax(v []float32) []float64 {
	out := make([]float64, len(v))
	shift := math.Inf(-1)
	for _, x := range v {
		if float64(x) > shift {
			shift = float64(x)
		}
	}
	var sum float64
	for i, x := range v {
		out[i] = math.Exp(float64(x) - shift)
		sum += out[i]
	}
	for i := range out {
		out[i] /= sum
	}
	return out
}

func argmax(v []float32) int {
	best := 0
	for i, x := range v {
		if x > v[best] {
			best = i
		}
	}
	return best
}
