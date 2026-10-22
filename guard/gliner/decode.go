package gliner

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

const (
	pySpace = `\t\n\v\f\r \x{001c}-\x{001f}\x{0085}\x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}`
	pyWord  = `[\p{L}\p{N}_\p{M}\x{200c}\x{200d}]`

	wordExpr = `(?i)(?:https?://[^` + pySpace + `]+|www\.[^` + pySpace + `]+)` +
		`|[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}` +
		`|@[a-z0-9_]+` +
		`|` + pyWord + `+(?:[-_]` + pyWord + `+)*` +
		`|[^` + pySpace + `]`
)

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

func decode(cfg *modelConfig, p *prepared, probs, countLogits []float32, clsLogits [][]float32) decodeResult {
	var res decodeResult
	labels := cfg.Schema.Entities.Labels
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
	res.classifications = decodeClassifications(cfg, clsLogits)
	if len(cfg.Schema.Classifications) > 0 && cfg.Schema.Classifications[0].activation() == "softmax" && len(clsLogits) > 0 {
		g := &cfg.Schema.Classifications[0]
		probsCls := softmax(clsLogits[0])
		best := argmax64(probsCls)
		res.safety = g.Labels[best]
		res.score = probsCls[best]
	}
	return res
}

func decodeClassifications(cfg *modelConfig, logits [][]float32) []decodedClass {
	groups := cfg.Schema.Classifications
	var out []decodedClass
	for gi := range groups {
		if gi >= len(logits) {
			break
		}
		g := &groups[gi]
		if g.MultiLabel {
			probs := sigmoid(logits[gi])
			for li, label := range g.Labels {
				if li >= len(probs) {
					break
				}
				if probs[li] >= g.thresholdFor(label) {
					out = append(out, decodedClass{group: gi, label: label, score: probs[li]})
				}
			}
			continue
		}
		probs := softmax(logits[gi])
		best := argmax64(probs)
		if best < len(g.Labels) {
			out = append(out, decodedClass{group: gi, label: g.Labels[best], score: probs[best]})
		}
	}
	return out
}

func sigmoid(v []float32) []float64 {
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = 1 / (1 + math.Exp(-float64(x)))
	}
	return out
}

func argmax64(v []float64) int {
	best := 0
	for i, x := range v {
		if x > v[best] {
			best = i
		}
	}
	return best
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
