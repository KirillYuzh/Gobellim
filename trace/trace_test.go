package trace

import (
	"context"
	"testing"
)

func TestNoopProviderKeepsContext(t *testing.T) {
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "v")

	got, span := Noop().Start(ctx, "chat", String("k", "v"))
	if got != ctx {
		t.Fatalf("Start changed the context: got %v, want the context it was given", got)
	}
	if span == nil {
		t.Fatal("Start returned a nil span")
	}
	// Every method of a no-op span must be safe to call.
	span.SetAttributes(String("k", "v"))
	span.SetError()
	span.RecordError(context.Canceled)
	span.Event("e")
	span.SetTokens(1, 2)
	span.End()
}

func TestAttributeConstructors(t *testing.T) {
	cases := []struct {
		name string
		attr Attribute
		key  string
		val  any
	}{
		{"Int", Int("i", 1), "i", 1},
		{"Int64", Int64("i64", 2), "i64", int64(2)},
		{"Float64", Float64("f", 1.5), "f", 1.5},
		{"String", String("s", "x"), "s", "x"},
		{"Bool", Bool("b", true), "b", true},
		{"Attr", Attr("a", []string{"x"}), "a", []string{"x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.attr.Key != tc.key {
				t.Errorf("Key = %q, want %q", tc.attr.Key, tc.key)
			}
			switch want := tc.val.(type) {
			case []string:
				got, ok := tc.attr.Value.([]string)
				if !ok {
					t.Fatalf("Value = %T, want []string", tc.attr.Value)
				}
				if len(got) != len(want) || got[0] != want[0] {
					t.Errorf("Value = %v, want %v", got, want)
				}
			default:
				if tc.attr.Value != want {
					t.Errorf("Value = %v (%T), want %v (%T)", tc.attr.Value, tc.attr.Value, want, want)
				}
			}
		})
	}
}

func TestContentModeString(t *testing.T) {
	cases := []struct {
		mode ContentMode
		want string
	}{
		{ContentRedacted, "redacted"},
		{ContentNone, "none"},
		{ContentFull, "full"},
		{ContentMode(99), "unknown"},
	}
	for _, tc := range cases {
		if got := ContentModeString(tc.mode); got != tc.want {
			t.Errorf("ContentModeString(%d) = %q, want %q", tc.mode, got, tc.want)
		}
	}
}

// The zero value of ContentMode must be the safe one: masked content, not
// "no content" and not raw content.
func TestContentRedactedIsTheDefault(t *testing.T) {
	var mode ContentMode
	if mode != ContentRedacted {
		t.Fatalf("the zero ContentMode is %v, want ContentRedacted", mode)
	}
	if !mode.Valid() {
		t.Error("the zero ContentMode is not valid")
	}
}

func TestContentModeValid(t *testing.T) {
	cases := []struct {
		mode ContentMode
		want bool
	}{
		{ContentRedacted, true},
		{ContentNone, true},
		{ContentFull, true},
		{ContentMode(-1), false},
		{ContentMode(99), false},
	}
	for _, tc := range cases {
		if got := tc.mode.Valid(); got != tc.want {
			t.Errorf("ContentMode(%d).Valid() = %v, want %v", tc.mode, got, tc.want)
		}
	}
}

func TestFinishReasonIsAList(t *testing.T) {
	attr := FinishReason("stop")
	if attr.Key != AttrFinishReason {
		t.Errorf("Key = %q, want %q", attr.Key, AttrFinishReason)
	}
	got, ok := attr.Value.([]string)
	if !ok {
		t.Fatalf("Value = %T, want []string", attr.Value)
	}
	if len(got) != 1 || got[0] != "stop" {
		t.Errorf("Value = %v, want [stop]", got)
	}
}

// The GenAI semantic conventions define no total token attribute: the total is
// the sum of the input and the output tokens.
func TestTokensHasNoTotal(t *testing.T) {
	attrs := Tokens(3, 4)
	if len(attrs) != 2 {
		t.Fatalf("Tokens returned %d attributes, want 2", len(attrs))
	}
	want := map[string]int{AttrInputTokens: 3, AttrOutputTokens: 4}
	for _, attr := range attrs {
		val, ok := attr.Value.(int)
		if !ok {
			t.Fatalf("%s = %T, want int", attr.Key, attr.Value)
		}
		if w, ok := want[attr.Key]; !ok {
			t.Errorf("unexpected attribute %q", attr.Key)
		} else if w != val {
			t.Errorf("%s = %d, want %d", attr.Key, val, w)
		}
	}
}
