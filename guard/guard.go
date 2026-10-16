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

	// Substitutions carries the values the mask replaced, so a caller can
	// rehydrate them for a tool that opted in. It is set only when Action is
	// Mask. Values are raw user data: never trace or log them.
	Substitutions []Substitution

	// Degraded is true when an optional layer failed and the verdict was
	// produced by the remaining layers. DegradedReason holds the layer errors.
	Degraded       bool
	DegradedReason string
}

// Substitution is one masked span: the placeholder written into the text and the
// value it replaced. The value is tagged out of JSON so it cannot leave through
// a serialised verdict.
type Substitution struct {
	Placeholder string
	Category    Category
	Value       string `json:"-"`
}

// withoutSubstitutions drops the raw values, for the paths where the verdict
// leaves the pipeline through a callback.
func (v Verdict) withoutSubstitutions() Verdict {
	if len(v.Substitutions) == 0 {
		return v
	}
	v.Substitutions = nil
	return v
}

type Detector interface {
	Detect(ctx context.Context, text string) ([]Finding, error)
	Name() string
}

const (
	DefaultMaxChars  = 8000
	DefaultCacheSize = 4096
	// DefaultWindowChars is the largest input a single detection pass inspects.
	// Longer text is scanned in overlapping windows of this size, so the ML layer
	// is not blind to the tail. Sized for the default L2 budget (512 tokens) at a
	// conservative ~2 characters per token.
	DefaultWindowChars = 1024
)

type Options struct {
	Layers []Detector
	// Optional lists layer names whose failure does not fail the check. An
	// optional layer that errors is skipped and the verdict is marked
	// Degraded; a required layer that errors fails the check. Empty means
	// every layer is required (fail-closed).
	Optional  []string
	MaxChars  int
	CacheSize int
	// WindowChars is the detection window. Text longer than it is scanned in
	// overlapping windows so the tail is not skipped; zero means DefaultWindowChars.
	WindowChars int
	Version     string
	// OnViolation, if set, is called for every non-allow verdict. The verdict it
	// receives carries no Substitutions, so masked values never leave here.
	OnViolation func(Verdict)
}

type Pipeline struct {
	layers      []Detector
	optional    map[string]bool
	maxChars    int
	windowChars int
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
	if opts.WindowChars <= 0 {
		opts.WindowChars = DefaultWindowChars
	}
	optional := make(map[string]bool, len(opts.Optional))
	for _, name := range opts.Optional {
		optional[name] = true
	}
	return &Pipeline{
		layers:      opts.Layers,
		optional:    optional,
		maxChars:    opts.MaxChars,
		windowChars: opts.WindowChars,
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
	var degraded []string
	if !ok {
		fs, deg, err := p.detect(ctx, inspected, utf8.RuneCountInString(text))
		if err != nil {
			return Verdict{}, err
		}
		findings = fs
		degraded = deg
		if len(degraded) == 0 {
			p.cache.put(key, findings)
		}
	}

	v := decide(rail, text, findings)
	v.Version = p.version
	v.Degraded = len(degraded) > 0
	v.DegradedReason = strings.Join(degraded, "; ")
	if v.Action != Allow && p.onViolation != nil {
		p.onViolation(v.withoutSubstitutions())
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

func (p *Pipeline) detect(ctx context.Context, inspected string, total int) ([]Finding, []string, error) {
	canonical := Canonicalize(inspected)

	var findings []Finding
	var degraded []string
	failed := make(map[string]bool)
	for _, w := range p.windows(canonical) {
		for _, layer := range p.layers {
			if failed[layer.Name()] {
				continue
			}
			fs, err := layer.Detect(ctx, w.text)
			if err != nil {
				if p.optional[layer.Name()] {
					failed[layer.Name()] = true
					degraded = append(degraded, layer.Name()+": "+err.Error())
					continue
				}
				return nil, nil, fmt.Errorf("guard: layer %s: %w", layer.Name(), err)
			}
			for _, f := range fs {
				f.Start += w.offset
				f.End += w.offset
				findings = append(findings, f)
			}
			if rejectCapable(fs) {
				return dedupe(findings), degraded, nil
			}
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
				if p.optional[layer.Name()] {
					degraded = append(degraded, layer.Name()+" (decoded): "+err.Error())
					continue
				}
				return nil, nil, fmt.Errorf("guard: layer %s (decoded): %w", layer.Name(), err)
			}
			for _, f := range fs {
				f.Start = 0
				f.End = total
				f.Layer = "L1+decode"
				findings = append(findings, f)
			}
		}
	}
	return dedupe(findings), degraded, nil
}

type window struct {
	text   string
	offset int // rune offset of the window in the canonical text
}

// windows splits the canonical text into overlapping windows, so a long input is
// scanned end to end instead of being cut off after the first window. Overlap
// covers a span that would otherwise be split across a boundary.
func (p *Pipeline) windows(canonical string) []window {
	runes := []rune(canonical)
	if p.windowChars <= 0 || len(runes) <= p.windowChars {
		return []window{{text: canonical}}
	}
	overlap := p.windowChars / 8
	if overlap < 1 {
		overlap = 1
	}
	var out []window
	for start := 0; start < len(runes); {
		end := start + p.windowChars
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, window{text: string(runes[start:end]), offset: start})
		if end >= len(runes) {
			break
		}
		start = end - overlap
	}
	return out
}

// dedupe drops findings repeated across overlapping windows.
func dedupe(findings []Finding) []Finding {
	if len(findings) < 2 {
		return findings
	}
	seen := make(map[Finding]struct{}, len(findings))
	out := findings[:0]
	for _, f := range findings {
		if _, ok := seen[f]; ok {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	return out
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
		v.Text, v.Substitutions = maskRunes(text, mergeMaskSpans(findings))
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

func maskRunes(text string, spans []Finding) (string, []Substitution) {
	if len(spans) == 0 {
		return text, nil
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].Start < spans[j].Start })

	runes := []rune(text)
	var b strings.Builder
	var subs []Substitution
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
		if end < s.Start {
			continue
		}
		b.WriteString(string(runes[prev:s.Start]))
		placeholder := "[" + string(s.Category) + ":" + s.Label + "]"
		b.WriteString(placeholder)
		if value := string(runes[s.Start:end]); value != "" {
			subs = append(subs, Substitution{Placeholder: placeholder, Category: s.Category, Value: value})
		}
		prev = end
	}
	if prev < len(runes) {
		b.WriteString(string(runes[prev:]))
	}
	return b.String(), subs
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
