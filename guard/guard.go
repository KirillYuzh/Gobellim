// Package guard runs the guard rail pipeline L0, L1, L2 with fast-fail on a
// reject verdict.
package guard

import (
	"container/list"
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
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
	Mask
	Reject
)

func (a Action) String() string {
	switch a {
	case Allow:
		return "allow"
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

type Verdict struct {
	Action   Action
	Rail     Rail
	Findings []Finding
	Text     string
}

// Detector is one guard layer.
type Detector interface {
	Detect(ctx context.Context, rail Rail, text string) ([]Finding, error)
	Name() string
}

const (
	DefaultMaxChars  = 8000
	DefaultCacheSize = 4096
)

type Options struct {
	Layers    []Detector
	MaxChars  int
	CacheSize int
}

type Pipeline struct {
	layers   []Detector
	maxChars int
	cache    *lru
}

func New(opts Options) (*Pipeline, error) {
	if opts.MaxChars <= 0 {
		opts.MaxChars = DefaultMaxChars
	}
	if opts.CacheSize <= 0 {
		opts.CacheSize = DefaultCacheSize
	}
	return &Pipeline{
		layers:   opts.Layers,
		maxChars: opts.MaxChars,
		cache:    newLRU(opts.CacheSize),
	}, nil
}

// Check runs the pipeline for one rail. A detector error aborts the check so
// the caller can fail closed.
func (p *Pipeline) Check(ctx context.Context, rail Rail, text string) (Verdict, error) {
	if text == "" {
		return Verdict{Action: Allow, Rail: rail, Text: text}, nil
	}
	key := string(rail) + "\x00" + text
	if v, ok := p.cache.get(key); ok {
		return v, nil
	}

	inspected := text
	if p.maxChars > 0 {
		if r := []rune(text); len(r) > p.maxChars {
			inspected = string(r[:p.maxChars])
		}
	}

	var findings []Finding
	for _, layer := range p.layers {
		fs, err := layer.Detect(ctx, rail, inspected)
		if err != nil {
			return Verdict{}, fmt.Errorf("guard: rail %s layer %s: %w", rail, layer.Name(), err)
		}
		findings = append(findings, fs...)
		if rejects(rail, fs) {
			break
		}
	}

	v := decide(rail, text, findings)
	p.cache.put(key, v)
	return v, nil
}

func rejects(rail Rail, fs []Finding) bool {
	for _, f := range fs {
		if actionFor(rail, f.Category) == Reject {
			return true
		}
	}
	return false
}

func actionFor(rail Rail, cat Category) Action {
	switch cat {
	case CatInjection, CatUnsafe:
		if rail == RailOutput {
			return Mask
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
		switch actionFor(rail, f.Category) {
		case Reject:
			action = Reject
		case Mask:
			if action == Allow {
				action = Mask
			}
		}
		if action == Reject {
			break
		}
	}
	v := Verdict{Action: action, Rail: rail, Findings: findings, Text: text}
	if action == Mask {
		spans := make([]Finding, 0, len(findings))
		for _, f := range findings {
			if actionFor(rail, f.Category) == Mask {
				spans = append(spans, f)
			}
		}
		v.Text = maskRunes(text, spans)
	}
	return v
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

type AllowAllDetector struct{}

func (AllowAllDetector) Name() string { return "allow-all" }

func (AllowAllDetector) Detect(context.Context, Rail, string) ([]Finding, error) {
	return nil, nil
}

// AllowAll returns a detector that never reports a finding. It is for local
// development only and disables fail-closed behaviour.
func AllowAll() Detector { return AllowAllDetector{} }

type lru struct {
	mu   sync.Mutex
	cap  int
	ll   *list.List
	item map[string]*list.Element
}

type lruEntry struct {
	key string
	val Verdict
}

func newLRU(capacity int) *lru {
	return &lru{cap: capacity, ll: list.New(), item: make(map[string]*list.Element, capacity)}
}

func (c *lru) get(key string) (Verdict, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.item[key]; ok {
		c.ll.MoveToFront(el)
		return el.Value.(*lruEntry).val, true
	}
	return Verdict{}, false
}

func (c *lru) put(key string, v Verdict) {
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
