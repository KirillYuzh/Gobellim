package guard

import (
	"container/list"
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

type Rail string

const (
	RailInput      Rail = "input"
	RailRetrieval  Rail = "retrieval"
	RailExecution  Rail = "execution"
	RailOutput     Rail = "output"
	RailToolResult Rail = "tool_result"
)

type Action int

const (
	Allow Action = iota
	Detect
	Mask
	Reject
)

func (a Action) String() string {
	switch a {
	case Allow:
		return "allow"
	case Detect:
		return "detect"
	case Mask:
		return "mask"
	case Reject:
		return "reject"
	default:
		return "unknown"
	}
}

type Category string

const (
	CatPII       Category = "PII"
	CatSecret    Category = "SECRET"
	CatInjection Category = "INJECTION"
	CatUnsafe    Category = "UNSAFE"
)

type Finding struct {
	Category Category
	Label    string
	Start    int
	End      int
	Score    float64
	Layer    string
}

type Tripwire struct {
	Reason   string
	RuleID   string
	Metadata map[string]any
}

type Verdict struct {
	Action   Action
	Rail     Rail
	Findings []Finding
	Text     string
	Tripwire *Tripwire
	Version  string
}

type Detector interface {
	Detect(ctx context.Context, text string) ([]Finding, error)
	Name() string
}

const (
	DefaultMaxChars  = 8000
	DefaultCacheSize = 4096
)

type Options struct {
	Layers      []Detector
	MaxChars    int
	CacheSize   int
	Version     string
	OnViolation func(Verdict)
}

type Pipeline struct {
	layers      []Detector
	maxChars    int
	cache       *lru
	version     string
	onViolation func(Verdict)
}

func New(opts Options) (*Pipeline, error) {
	if opts.MaxChars <= 0 {
		opts.MaxChars = DefaultMaxChars
	}
	if opts.CacheSize <= 0 {
		opts.CacheSize = DefaultCacheSize
	}
	return &Pipeline{
		layers:      opts.Layers,
		maxChars:    opts.MaxChars,
		cache:       newLRU(opts.CacheSize),
		version:     opts.Version,
		onViolation: opts.OnViolation,
	}, nil
}

func (p *Pipeline) Version() string { return p.version }

func (p *Pipeline) Check(ctx context.Context, rail Rail, text string) (Verdict, error) {
	if text == "" {
		return Verdict{Action: Allow, Rail: rail, Text: text, Version: p.version}, nil
	}
	inspected := truncateRunes(text, p.maxChars)
	key := p.cacheKey(Canonicalize(inspected))

	findings, ok := p.cache.get(key)
	if !ok {
		fs, err := p.detect(ctx, inspected, utf8.RuneCountInString(text))
		if err != nil {
			return Verdict{}, err
		}
		findings = fs
		p.cache.put(key, findings)
	}

	v := decide(rail, text, findings)
	v.Version = p.version
	if v.Action != Allow && p.onViolation != nil {
		p.onViolation(v)
	}
	return v, nil
}

func (p *Pipeline) cacheKey(canonical string) string {
	h := sha256.New()
	h.Write([]byte(p.version))
	h.Write([]byte{0})
	h.Write([]byte(canonical))
	return string(h.Sum(nil))
}

func truncateRunes(text string, max int) string {
	if max <= 0 {
		return text
	}
	if r := []rune(text); len(r) > max {
		return string(r[:max])
	}
	return text
}

func (p *Pipeline) detect(ctx context.Context, inspected string, total int) ([]Finding, error) {
	canonical := Canonicalize(inspected)

	var findings []Finding
	for _, layer := range p.layers {
		fs, err := layer.Detect(ctx, canonical)
		if err != nil {
			return nil, fmt.Errorf("guard: layer %s: %w", layer.Name(), err)
		}
		findings = append(findings, fs...)
		if rejectCapable(fs) {
			return findings, nil
		}
	}

	for _, variant := range DecodedVariants(inspected) {
		decoded := Canonicalize(variant)
		for _, layer := range p.layers {
			if layer.Name() != "L1" {
				continue
			}
			fs, err := layer.Detect(ctx, decoded)
			if err != nil {
				return nil, fmt.Errorf("guard: layer %s (decoded): %w", layer.Name(), err)
			}
			for _, f := range fs {
				f.Start = 0
				f.End = total
				f.Layer = "L1+decode"
				findings = append(findings, f)
			}
		}
	}
	return findings, nil
}

func rejectCapable(fs []Finding) bool {
	for _, f := range fs {
		if f.Category == CatInjection || f.Category == CatUnsafe {
			return true
		}
	}
	return false
}

func actionFor(rail Rail, cat Category) Action {
	switch cat {
	case CatInjection, CatUnsafe:
		if rail == RailOutput {
			return Detect
		}
		return Reject
	case CatPII, CatSecret:
		return Mask
	default:
		return Allow
	}
}

func decide(rail Rail, text string, findings []Finding) Verdict {
	action := Allow
	for _, f := range findings {
		if a := actionFor(rail, f.Category); a > action {
			action = a
		}
		if action == Reject {
			break
		}
	}
	v := Verdict{Action: action, Rail: rail, Findings: findings, Text: text}
	if action == Mask {
		v.Text = maskRunes(text, mergeMaskSpans(findings))
	}
	if action == Reject {
		v.Tripwire = tripwireFor(rail, findings)
	}
	return v
}

func mergeMaskSpans(findings []Finding) []Finding {
	spans := make([]Finding, 0, len(findings))
	for _, f := range findings {
		if f.Category == CatPII || f.Category == CatSecret {
			spans = append(spans, f)
		}
	}
	if len(spans) == 0 {
		return nil
	}
	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].Start != spans[j].Start {
			return spans[i].Start < spans[j].Start
		}
		return spans[i].End < spans[j].End
	})

	merged := make([]Finding, 0, len(spans))
	cur := spans[0]
	labels := []string{cur.Label}
	seen := map[string]bool{cur.Label: true}
	flush := func() {
		cur.Label = strings.Join(labels, ",")
		merged = append(merged, cur)
	}
	for _, f := range spans[1:] {
		if f.Start <= cur.End {
			if f.End > cur.End {
				cur.End = f.End
			}
			if f.Category == CatPII {
				cur.Category = CatPII
			}
			if !seen[f.Label] {
				seen[f.Label] = true
				labels = append(labels, f.Label)
			}
			continue
		}
		flush()
		cur = f
		labels = []string{f.Label}
		seen = map[string]bool{f.Label: true}
	}
	flush()
	return merged
}

func tripwireFor(rail Rail, findings []Finding) *Tripwire {
	var deciding *Finding
	labels := make([]string, 0, len(findings))
	categories := make([]string, 0, len(findings))
	seenLabel := make(map[string]bool, len(findings))
	seenCat := make(map[string]bool, len(findings))
	for i := range findings {
		f := &findings[i]
		if !seenLabel[f.Label] {
			seenLabel[f.Label] = true
			labels = append(labels, f.Label)
		}
		if !seenCat[string(f.Category)] {
			seenCat[string(f.Category)] = true
			categories = append(categories, string(f.Category))
		}
		if deciding == nil && actionFor(rail, f.Category) == Reject {
			deciding = f
		}
	}
	if deciding == nil {
		return nil
	}
	return &Tripwire{
		Reason: strings.Join(labels, ","),
		RuleID: deciding.Label + "/" + deciding.Layer,
		Metadata: map[string]any{
			"rail":       string(rail),
			"categories": categories,
		},
	}
}

func maskRunes(text string, spans []Finding) string {
	if len(spans) == 0 {
		return text
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].Start < spans[j].Start })

	runes := []rune(text)
	var b strings.Builder
	prev := 0
	for _, s := range spans {
		if s.Start < prev {
			continue
		}
		if s.Start > len(runes) {
			continue
		}
		end := s.End
		if end > len(runes) {
			end = len(runes)
		}
		b.WriteString(string(runes[prev:s.Start]))
		b.WriteString("[")
		b.WriteString(string(s.Category))
		b.WriteString(":")
		b.WriteString(s.Label)
		b.WriteString("]")
		prev = end
	}
	if prev < len(runes) {
		b.WriteString(string(runes[prev:]))
	}
	return b.String()
}

type lru struct {
	mu   sync.Mutex
	cap  int
	ll   *list.List
	item map[string]*list.Element
}

type lruEntry struct {
	key string
	val []Finding
}

func newLRU(capacity int) *lru {
	return &lru{cap: capacity, ll: list.New(), item: make(map[string]*list.Element, capacity)}
}

func (c *lru) get(key string) ([]Finding, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.item[key]; ok {
		c.ll.MoveToFront(el)
		return el.Value.(*lruEntry).val, true
	}
	return nil, false
}

func (c *lru) put(key string, v []Finding) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.item[key]; ok {
		c.ll.MoveToFront(el)
		el.Value.(*lruEntry).val = v
		return
	}
	el := c.ll.PushFront(&lruEntry{key: key, val: v})
	c.item[key] = el
	if c.ll.Len() > c.cap {
		oldest := c.ll.Back()
		if oldest != nil {
			c.ll.Remove(oldest)
			delete(c.item, oldest.Value.(*lruEntry).key)
		}
	}
}
