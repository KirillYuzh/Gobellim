package trace

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// TextPolicy selects what trace text is exported: TextMasked keeps only text
// that passed the guard, TextFull exports raw text and is an explicit opt-in.
type TextPolicy int

const (
	// TextMasked is the default policy: only text processed by the guard is kept.
	TextMasked TextPolicy = iota
	// TextFull exports unmodified input and output.
	TextFull
)

// String returns "masked" or "full".
func (p TextPolicy) String() string {
	if p == TextFull {
		return "full"
	}
	return "masked"
}

// IsRaw reports whether the policy exports raw text, i.e. it is TextFull.
func (p TextPolicy) IsRaw() bool { return p == TextFull }

// Sampler decides whether a trace with the given trace ID is recorded.
type Sampler func(traceID string) bool

var (
	// SamplerAlways records every trace.
	SamplerAlways Sampler = func(string) bool { return true }
	// SamplerNever records no traces.
	SamplerNever Sampler = func(string) bool { return false }
)

// SamplerRatio returns a deterministic sampler that records the fraction p of
// traces by trace ID; p <= 0 is SamplerNever and p >= 1 is SamplerAlways.
func SamplerRatio(p float64) Sampler {
	if p <= 0 {
		return SamplerNever
	}
	if p >= 1 {
		return SamplerAlways
	}
	return func(traceID string) bool {
		return unitFromTraceID(traceID) < p
	}
}

func unitFromTraceID(traceID string) float64 {
	const denom = 1 << 32
	if len(traceID) >= 8 {
		if b, err := hex.DecodeString(traceID[:8]); err == nil {
			return float64(binary.BigEndian.Uint32(b)) / denom
		}
	}
	sum := sha256.Sum256([]byte(traceID))
	return float64(binary.BigEndian.Uint32(sum[:4])) / denom
}

// SamplerFromString parses "always", "never" or "ratio:<float>".
func SamplerFromString(s string) (Sampler, error) {
	switch s {
	case "always":
		return SamplerAlways, nil
	case "never":
		return SamplerNever, nil
	}
	if rest, ok := strings.CutPrefix(s, "ratio:"); ok {
		p, err := strconv.ParseFloat(strings.TrimSpace(rest), 64)
		if err != nil {
			return nil, fmt.Errorf("trace: invalid sampler ratio %q: %w", s, err)
		}
		return SamplerRatio(p), nil
	}
	return nil, fmt.Errorf("trace: unknown sampler %q (want always, never or ratio:<float>)", s)
}
