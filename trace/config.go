package trace

// ContentMode selects how much of the message content a trace records.
//
// The mode is a selection, not a redaction engine: the guard produces the
// masked text, and the mode decides whether that masked text (or the raw one)
// is written to the span at all. ContentRedacted is the zero value, so an
// application that never configures a mode records masked content.
type ContentMode int

const (
	// ContentRedacted records the masked content the guard produced. It is the
	// default.
	ContentRedacted ContentMode = iota

	// ContentNone records metadata only: no prompt, no completion, no tool
	// content.
	ContentNone

	// ContentFull records the raw content. It is an explicit opt-in and is
	// discouraged for production.
	ContentFull
)

// Valid reports whether the mode is one of the defined values.
func (c ContentMode) Valid() bool {
	return c >= ContentRedacted && c <= ContentFull
}

// ContentModeString returns the string representation of ContentMode.
func ContentModeString(c ContentMode) string {
	switch c {
	case ContentRedacted:
		return "redacted"
	case ContentNone:
		return "none"
	case ContentFull:
		return "full"
	default:
		return "unknown"
	}
}
