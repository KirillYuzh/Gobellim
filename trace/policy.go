package trace

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

type TextPolicy int

const (
	TextMasked TextPolicy = iota
	TextFull
)

func (p TextPolicy) String() string {
	if p == TextFull {
		return "full"
	}
	return "masked"
}

func (p TextPolicy) IsRaw() bool { return p == TextFull }

type Sampler func(traceID string) bool

var (
	SamplerAlways Sampler = func(string) bool { return true }
	SamplerNever  Sampler = func(string) bool { return false }
)

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
