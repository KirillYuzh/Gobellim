package chunk

import (
	"strings"
	"testing"
	"unicode/utf8"
)

type runeTokenizer struct{}

func (runeTokenizer) Tokens(text string) []Token {
	var toks []Token
	i := 0
	for range text {
		toks = append(toks, Token{Start: i, End: i + 1})
		i++
	}
	return toks
}

func TestSplitWindows(t *testing.T) {
	c, err := New(runeTokenizer{}, 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	got := c.Split("abcdefgh")
	want := []string{"abcd", "cdef", "efgh"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chunk %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSplitNoOverlap(t *testing.T) {
	c, _ := New(runeTokenizer{}, 3, 0)
	got := c.Split("abcdef")
	want := []string{"abc", "def"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSplitShortText(t *testing.T) {
	c, _ := New(runeTokenizer{}, 10, 2)
	got := c.Split("abc")
	if len(got) != 1 || got[0] != "abc" {
		t.Fatalf("got %v", got)
	}
}

func TestSplitEmpty(t *testing.T) {
	c, _ := New(WhitespaceTokenizer{}, 4, 1)
	if got := c.Split("   "); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New(nil, 4, 1); err == nil {
		t.Fatal("nil tokenizer must fail")
	}
	if _, err := New(WhitespaceTokenizer{}, 0, 0); err == nil {
		t.Fatal("size 0 must fail")
	}
	if _, err := New(WhitespaceTokenizer{}, 4, 4); err == nil {
		t.Fatal("overlap == size must fail")
	}
}

func TestWhitespaceTokenizerOffsets(t *testing.T) {
	toks := WhitespaceTokenizer{}.Tokens("мама мыла раму")
	if len(toks) != 3 {
		t.Fatalf("tokens = %v", toks)
	}
	if toks[0].Start != 0 || toks[1].Start != 5 {
		t.Fatalf("unexpected offsets: %+v", toks)
	}

	if got := string([]rune("мама мыла раму")[toks[0].Start:toks[0].End]); got != "мама" {
		t.Fatalf("slice = %q", got)
	}
	if utf8.RuneCountInString("мама") != 4 {
		t.Fatal("sanity")
	}
}

// brokenTokenizer reports offsets outside the text. Tokenizer is a public
// interface, so a broken implementation must not crash the process.
type brokenTokenizer struct{ toks []Token }

func (b brokenTokenizer) Tokens(string) []Token { return b.toks }

func TestSplitSurvivesBrokenOffsets(t *testing.T) {
	cases := []struct {
		name string
		toks []Token
	}{
		{"start past the end", []Token{{Start: 1000, End: 2000}}},
		{"negative offsets", []Token{{Start: -5, End: -1}}},
		{"end before start", []Token{{Start: 2, End: 1}}},
		{"zero length", []Token{{Start: 1, End: 1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := New(brokenTokenizer{toks: tc.toks}, 4, 1)
			if err != nil {
				t.Fatal(err)
			}
			// The call must return, not panic.
			got := c.Split("abc")
			for _, g := range got {
				if !utf8.ValidString(g) {
					t.Fatalf("chunk %q is not valid UTF-8", g)
				}
			}
		})
	}
}
