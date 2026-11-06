package gobellim

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	handleRE = regexp.MustCompile(`^<h[0-9]+>$`)
	// handleTokenRE finds a handle inside a longer string, which is what the
	// output needs: the model may put one in a sentence.
	handleTokenRE = regexp.MustCompile(`<h[0-9]+>`)
)

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
// labels share a handle so a repeated sensitive value does not inflate the
// context. The key
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
