package gobellim

import (
	"fmt"
	"regexp"
	"strings"
)

var handleRE = regexp.MustCompile(`^<h[0-9]+>$`)

// piiPatterns are deliberately narrow. They catch common shapes and are not a
// DLP system: a match becomes a handle, a miss is not a guarantee of safety.
var piiPatterns = []*regexp.Regexp{
	regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`),
	regexp.MustCompile(`\b(?:\d[ \-]?){13,16}\b`),
	regexp.MustCompile(`\+?\d[\d ()\-]{8,}\d`),
	regexp.MustCompile(`\b\d{4}\s?\d{6}\b`),
}

type handleEntry struct {
	value string
	label Label
}

// handleSet maps opaque references to values for one task run. The real value
// is never inserted into a prompt or a log.
type handleSet struct {
	next    int
	byRef   map[string]handleEntry
	byValue map[string]string
}

func newHandleSet() *handleSet {
	return &handleSet{
		byRef:   make(map[string]handleEntry),
		byValue: make(map[string]string),
	}
}

// put stores value and returns its handle. Identical values with identical
// labels share a handle so repeated PII does not inflate the context. The key
// includes integrity and sources: two equal strings with different provenance
// must never collapse into one label.
func (h *handleSet) put(value string, l Label) string {
	key := labelKey(l) + "\x00" + value
	if ref, ok := h.byValue[key]; ok {
		return ref
	}
	h.next++
	ref := fmt.Sprintf("<h%d>", h.next)
	h.byRef[ref] = handleEntry{value: value, label: l}
	h.byValue[key] = ref
	return ref
}

func labelKey(l Label) string {
	var b strings.Builder
	b.WriteString(l.Integrity.String())
	b.WriteByte(0)
	b.WriteString(l.Sensitivity.String())
	for _, s := range l.Sources {
		b.WriteByte(0)
		b.WriteString(s.Kind)
		b.WriteByte(':')
		b.WriteString(s.ID)
	}
	return b.String()
}

func (h *handleSet) get(ref string) (handleEntry, bool) {
	e, ok := h.byRef[ref]
	return e, ok
}

func isHandleRef(s string) bool { return handleRE.MatchString(s) }

// maskPII replaces every match of the PII patterns with a handle. The original
// value stays in the handle set.
func maskPII(s string, l Label, hs *handleSet) string {
	out := s
	for _, re := range piiPatterns {
		out = re.ReplaceAllStringFunc(out, func(m string) string {
			return hs.put(m, l)
		})
	}
	return out
}
