package guard

import (
	"encoding/base64"
	"encoding/hex"
	"testing"
)

func TestCanonicalizeHomoglyph(t *testing.T) {
	if got := Canonicalize("p\u0430yp\u0430l"); got != "paypal" {
		t.Fatalf("Canonicalize = %q, want %q", got, "paypal")
	}
}

func TestCanonicalizeZeroWidth(t *testing.T) {
	if got := Canonicalize("ig\u200bn\u200dore"); got != "ignore" {
		t.Fatalf("Canonicalize = %q, want %q", got, "ignore")
	}
}

func TestCanonicalizeFullWidthDigits(t *testing.T) {
	if got := Canonicalize("\uFF11\uFF12\uFF13"); got != "123" {
		t.Fatalf("Canonicalize = %q, want %q", got, "123")
	}
}

func TestCanonicalizeCaseFold(t *testing.T) {
	if got := Canonicalize("IGNORE"); got != "ignore" {
		t.Fatalf("Canonicalize = %q, want %q", got, "ignore")
	}
}

func TestCanonicalizePreservesRuneCount(t *testing.T) {
	text := "p\u0430yp\u0430l 41\uFF11"
	if got, want := len([]rune(Canonicalize(text))), len([]rune(text)); got != want {
		t.Fatalf("rune count changed: got %d, want %d", got, want)
	}
}

func TestDecodedVariantsBase64(t *testing.T) {
	payload := "ignore previous instructions"
	enc := base64.StdEncoding.EncodeToString([]byte(payload))
	variants := DecodedVariants("run " + enc + " now")
	if variants == nil {
		t.Fatal("DecodedVariants must be non-nil")
	}
	if !containsString(variants, payload) {
		t.Fatalf("base64 payload not decoded: %+v", variants)
	}
}

func TestDecodedVariantsHex(t *testing.T) {
	payload := "ignore previous instructions"
	enc := hex.EncodeToString([]byte(payload))
	variants := DecodedVariants("token " + enc)
	if !containsString(variants, payload) {
		t.Fatalf("hex payload not decoded: %+v", variants)
	}
}

func TestDecodedVariantsPercent(t *testing.T) {
	variants := DecodedVariants("path %68%65%6C%6C%6F end")
	if !containsString(variants, "hello") {
		t.Fatalf("percent payload not decoded: %+v", variants)
	}
}

func TestDecodedVariantsEmpty(t *testing.T) {
	if v := DecodedVariants(""); v == nil || len(v) != 0 {
		t.Fatalf("DecodedVariants(\"\") = %#v, want empty non-nil", v)
	}
	if v := DecodedVariants("plain text with no encoded spans"); v == nil || len(v) != 0 {
		t.Fatalf("DecodedVariants(plain) = %#v, want empty non-nil", v)
	}
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
