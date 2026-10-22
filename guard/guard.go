package guard

import (
	"container/list"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

// Action is the decision a verdict carries, ordered from Allow (least severe)
// to Reject (most severe).
type Action int

const (
	// Allow accepts the operation and leaves the text unchanged.
	Allow Action = iota
	// Detect records the finding without changing the text or blocking the
	// operation; it is used for injection/unsafe findings on the output rail.
	Detect
	// Mask rewrites PII and SECRET spans in Verdict.Text with placeholders.
	Mask
	// Reject blocks the operation and fills Verdict.Tripwire.
	Reject
)

// String returns the lowercase action name, or "unknown" for an out-of-range value.
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

// Category is the kind of finding; it selects the action for a rail in the decision table.
type Category string

const (
	// CatPII marks personally identifiable information; it maps to Mask.
	CatPII Category = "PII"
	// CatSecret marks credentials and tokens; it maps to Mask.
	CatSecret Category = "SECRET"
	// CatInjection marks prompt-injection attempts; it maps to Reject, or Detect
	// on the output rail.
	CatInjection Category = "INJECTION"
	// CatUnsafe marks unsafe or harmful content; it maps to Reject, or Detect on
	// the output rail.
	CatUnsafe Category = "UNSAFE"
)

// Finding is one detector hit: a category and label at a rune span in the
// original text, with the detector's score and layer name.
type Finding struct {
	Category Category
	Label    string
	Start    int
	End      int
	Score    float64
	Layer    string
}

// Tripwire explains a Reject verdict: the deciding labels, the rule and layer
// that fired, and rail/category metadata.
type Tripwire struct {
	Reason   string
	RuleID   string
	Metadata map[string]any
}

// Verdict is the result of Pipeline.Check: the action, the findings that drove
// it, the possibly masked text, and provenance such as version, degradation,
// and truncation.
type Verdict struct {
	Action   Action
	Rail     Rail
	Findings []Finding
	Text     string
	Tripwire *Tripwire
	Version  string

	Substitutions []Substitution

	Degraded       bool
	DegradedReason string

	Truncated      bool
	InspectedRunes int
}

// ObservationMetadata reports truncation for the trace observation, or nil when
// the whole text was inspected.
func (v Verdict) ObservationMetadata() map[string]any {
	if !v.Truncated {
		return nil
	}
	return map[string]any{
		"truncated":       true,
		"inspected_chars": v.InspectedRunes,
	}
}

// Substitution records a value replaced by Mask so a caller may restore it for
// tool arguments under AllowPII.
type Substitution struct {
	Placeholder string
	Category    Category
	Value       string `json:"-"`
}

func (v Verdict) withoutSubstitutions() Verdict {
	if len(v.Substitutions) == 0 {
		return v
	}
	v.Substitutions = nil
	return v
}

// Detector is one guard layer: Detect returns findings for text and Name
// identifies the layer; the rail is not passed to detectors.
type Detector interface {
	Detect(ctx context.Context, text string) ([]Finding, error)
	Name() string
}

// NaturalText is an optional Detector capability: when UsesNaturalText reports
// true the detector receives the original text instead of the canonical form.
type NaturalText interface {
	Detector
	UsesNaturalText() bool
}

func usesNaturalText(d Detector) bool {
	n, ok := d.(NaturalText)
	return ok && n.UsesNaturalText()
}

const (
	// DefaultMaxChars is the default cap on inspected runes.
	DefaultMaxChars = 8000
	// DefaultCacheSize is the default number of cached findings.
	DefaultCacheSize = 4096
	// DefaultWindowChars is the default detector window; longer text is scanned
	// in overlapping windows.
	DefaultWindowChars = 1024
)

// Options configures New: detector layers and ensembles, per-rail optional
// layers for degradation, limits and window size, versioning, and callbacks.
type Options struct {
	Layers               []Detector
	Ensembles            []Ensemble
	OptionalByRail       map[Rail][]string
	MaxChars             int
	CacheSize            int
	WindowChars          int
	Version              string
	ArtifactHash         string
	OnViolation          func(Verdict)
	Logger               *slog.Logger
	DegradedWarnInterval time.Duration

	now func() time.Time
}

// Pipeline evaluates the configured guard layers for a rail and produces a
// Verdict; create it with New and call Check.
type Pipeline struct {
	layers      []layer
	degradation degradationPolicy
	maxChars    int
	windowChars int
	cache       *lru
	version     string
	onViolation func(Verdict)
	warner      *warner
	counters    counters
}

// New builds a Pipeline from opts, applying defaults for limits, cache size,
// and window, and rejecting duplicate or invalid layer configuration.
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
	layers, err := buildLayers(opts.Layers, opts.Ensembles)
	if err != nil {
		return nil, err
	}
	degradation, err := newDegradationPolicy(opts.OptionalByRail)
	if err != nil {
		return nil, err
	}
	if opts.DegradedWarnInterval <= 0 {
		opts.DegradedWarnInterval = DefaultDegradedWarnInterval
	}
	artifact := opts.ArtifactHash
	if artifact == "" {
		detectors := make([]Detector, 0, len(layers))
		for _, l := range layers {
			detectors = append(detectors, l.detectors...)
		}
		artifact = ArtifactHashes(detectors)
	}
	return &Pipeline{
		layers:      layers,
		degradation: degradation,
		maxChars:    opts.MaxChars,
		windowChars: opts.WindowChars,
		cache:       newLRU(opts.CacheSize),
		version:     computeVersion(opts, layers, artifact),
		onViolation: opts.OnViolation,
		warner:      newWarner(opts.Logger, opts.DegradedWarnInterval, opts.now),
	}, nil
}

func (p *Pipeline) optionalOn(rail Rail, l layer, d Detector) bool {
	return p.degradation.allows(rail, l.name, d.Name())
}

// Version returns the guard version recorded in every verdict, built from the
// configured or derived version plus the detector artifact hash.
func (p *Pipeline) Version() string { return p.version }

// Check runs every configured layer over text for the rail and returns a
// Verdict. Text is truncated to MaxChars, L1 reads the canonical form, decoded
// variants are rescanned through L1, and a required detector error is returned.
func (p *Pipeline) Check(ctx context.Context, rail Rail, text string) (Verdict, error) {
	if !ValidRail(rail) {
		return Verdict{}, fmt.Errorf("guard: unknown rail %q: %w", string(rail), core.ErrInvalidConfig)
	}
	if err := ctx.Err(); err != nil {
		return Verdict{}, err
	}
	started := time.Now()
	total := utf8.RuneCountInString(text)
	if text == "" {
		v := p.finish(rail, text, nil, nil, 0, total)
		p.observeLatency(time.Since(started))
		return v, nil
	}
	inspectedText := truncateRunes(text, p.maxChars)
	inspected := utf8.RuneCountInString(inspectedText)
	canonical, spans := canonicalizeMap(inspectedText)
	key := p.cacheKey(inspectedText)

	findings, ok := p.cache.get(key)
	var degraded []string
	if !ok {
		fs, deg, err := p.detect(ctx, rail, inspectedText, canonical, spans, total)
		if err != nil {
			return Verdict{}, err
		}
		findings = fs
		degraded = deg
		if len(degraded) == 0 {
			p.cache.put(key, findings)
		}
	}

	v := p.finish(rail, text, findings, degraded, inspected, total)
	p.observeLatency(time.Since(started))
	return v, nil
}

func (p *Pipeline) finish(rail Rail, text string, findings []Finding, degraded []string, inspected, total int) Verdict {
	v := decide(rail, text, findings)
	v.Version = p.version
	v.Degraded = len(degraded) > 0
	v.DegradedReason = strings.Join(degraded, "; ")
	v.Truncated = inspected < total
	v.InspectedRunes = inspected

	// Fail closed on truncation for input rail: if the text was truncated,
	// we cannot guarantee the uninspected portion is safe.
	if rail == RailInput && v.Truncated {
		v.Action = Reject
		v.Tripwire = &Tripwire{
			Reason: "input truncated, uninspected content may contain PII or injection",
			RuleID: "truncation",
			Metadata: map[string]any{
				"rail":            string(rail),
				"inspected_runes": inspected,
				"total_runes":     total,
			},
		}
	}

	p.count(rail, v)
	if v.Degraded {
		p.warner.warn(rail, v.Version, v.DegradedReason)
	}
	if v.Action != Allow && p.onViolation != nil {
		p.onViolation(v.withoutSubstitutions())
	}
	return v
}

func (p *Pipeline) cacheKey(inspected string) string {
	h := sha256.New()
	h.Write([]byte(p.version))
	h.Write([]byte{0})
	h.Write([]byte(inspected))
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

func (p *Pipeline) detect(ctx context.Context, r Rail, inspected, canonical string, spans runeSpans, total int) ([]Finding, []string, error) {
	var findings []Finding
	var degraded []string

	for _, l := range p.layers {
		for _, d := range l.detectors {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			raw := usesNaturalText(d)
			stream := canonical
			if raw {
				stream = inspected
			}
			for _, w := range p.windows(stream) {
				fs, err := d.Detect(ctx, w.text)
				if err != nil {
					if p.optionalOn(r, l, d) {
						degraded = append(degraded, degradedLabel(l, d, err))
						break
					}
					return nil, nil, fmt.Errorf("guard: layer %s: %w", degradedLabel(l, d, err), err)
				}
				for _, f := range fs {
					if raw {
						f.Start += w.offset
						f.End += w.offset
					} else {
						f.Start, f.End = remapSpan(spans, f.Start+w.offset, f.End+w.offset, total)
					}
					findings = append(findings, f)
				}
				if rejectCapable(fs) {
					return dedupe(findings), degraded, nil
				}
			}
		}
	}

	for _, variant := range DecodedVariants(inspected) {
		decoded := Canonicalize(variant)
		for _, l := range p.layers {
			if l.name != rulesLayer {
				continue
			}
			for _, d := range l.detectors {
				fs, err := d.Detect(ctx, decoded)
				if err != nil {
					if p.optionalOn(r, l, d) {
						degraded = append(degraded, degradedLabel(l, d, err)+" (decoded)")
						continue
					}
					return nil, nil, fmt.Errorf("guard: layer %s (decoded): %w", degradedLabel(l, d, err), err)
				}
				for _, f := range fs {
					f.Start = 0
					f.End = total
					f.Layer = rulesLayer + "+decode"
					findings = append(findings, f)
				}
			}
		}
	}
	return dedupe(findings), degraded, nil
}

func degradedLabel(l layer, d Detector, err ...error) string {
	name := l.name
	if l.name != d.Name() || len(l.detectors) > 1 {
		name = l.name + "/" + d.Name()
	}
	if len(err) > 0 && err[0] != nil {
		switch {
		case errors.Is(err[0], context.Canceled):
			name += ":canceled"
		case errors.Is(err[0], context.DeadlineExceeded):
			name += ":deadline"
		case errors.Is(err[0], core.ErrGuardUnavailable):
			name += ":unavailable"
		default:
			name += ":error"
		}
	}
	return name
}

func remapSpan(sp runeSpans, start, end, total int) (int, int) {
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if start >= sp.len() {
		return total, total
	}
	if end > sp.len() {
		end = sp.len()
	}
	if end <= start {
		return sp.start[start], sp.start[start]
	}
	return sp.start[start], sp.end[end-1]
}

type window struct {
	text   string
	offset int
}

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
	counters := make(map[string]int)
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
		key := string(s.Category) + ":" + s.Label
		counters[key]++
		placeholder := "[" + key + ":" + strconv.Itoa(counters[key]) + "]"
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
		val := el.Value.(*lruEntry).val
		out := make([]Finding, len(val))
		copy(out, val)
		return out, true
	}
	return nil, false
}

func (c *lru) put(key string, v []Finding) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// get returns a copy, so put must store a copy too: a caller mutating the
	// slice it got from a Verdict must not poison the next verdict.
	cp := make([]Finding, len(v))
	copy(cp, v)
	if el, ok := c.item[key]; ok {
		c.ll.MoveToFront(el)
		el.Value.(*lruEntry).val = cp
		return
	}
	el := c.ll.PushFront(&lruEntry{key: key, val: cp})
	c.item[key] = el
	if c.ll.Len() > c.cap {
		oldest := c.ll.Back()
		if oldest != nil {
			c.ll.Remove(oldest)
			delete(c.item, oldest.Value.(*lruEntry).key)
		}
	}
}
