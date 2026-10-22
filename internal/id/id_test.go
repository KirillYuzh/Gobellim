package id

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestNewFormatAndUniqueness(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		v := New()
		if len(v) != 32 {
			t.Fatalf("New() = %q, want 32 hex chars", v)
		}
		if _, err := hex.DecodeString(v); err != nil {
			t.Fatalf("New() = %q is not hex: %v", v, err)
		}
		if seen[v] {
			t.Fatalf("duplicate id %q", v)
		}
		seen[v] = true
	}
}

func TestUUIDv4Format(t *testing.T) {
	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		v := UUIDv4()
		if len(v) != 36 {
			t.Fatalf("UUIDv4() = %q, want 36 chars", v)
		}
		for _, at := range []int{8, 13, 18, 23} {
			if v[at] != '-' {
				t.Fatalf("UUIDv4() = %q: want '-' at %d", v, at)
			}
		}
		raw, err := hex.DecodeString(strings.ReplaceAll(v, "-", ""))
		if err != nil {
			t.Fatalf("UUIDv4() = %q is not hex: %v", v, err)
		}
		if got := raw[6] >> 4; got != 4 {
			t.Fatalf("UUIDv4() = %q: version = %d, want 4", v, got)
		}
		if got := raw[8] >> 6; got != 2 {
			t.Fatalf("UUIDv4() = %q: variant bits = %02b, want 10", v, got)
		}
		if seen[v] {
			t.Fatalf("duplicate uuid %q", v)
		}
		seen[v] = true
	}
}

func TestFallbackHex(t *testing.T) {
	a, b := fallbackHex(32), fallbackHex(32)
	if len(a) != 32 || len(b) != 32 {
		t.Fatalf("fallbackHex(32) = %q, %q; want 32 chars", a, b)
	}
	if a == b {
		t.Fatalf("fallbackHex returned the same value twice: %q", a)
	}
	if _, err := hex.DecodeString(a); err != nil {
		t.Fatalf("fallbackHex(32) = %q is not hex: %v", a, err)
	}
}
