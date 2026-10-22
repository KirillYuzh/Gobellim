package chunk

import (
	"fmt"
	"unicode"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

const (
	// DefaultSize is the default number of tokenizer units per chunk.
	DefaultSize = 400
	// DefaultOverlap is the default number of tokenizer units shared by
	// adjacent chunks.
	DefaultOverlap = 80
)

// Token is a tokenizer unit with rune offsets into the original text; Start is
// inclusive and End is exclusive. Only the offsets are needed to cut the text,
// so a tokenizer does not have to allocate the token text itself.
type Token struct {
	Start int
	End   int
}

// Tokenizer splits text into tokens carrying rune offsets into that text.
type Tokenizer interface {
	Tokens(text string) []Token
}

// Chunker splits text into overlapping windows of tokenizer units.
type Chunker struct {
	tok     Tokenizer
	size    int
	overlap int
}

// New validates the tokenizer, size and overlap and returns a Chunker; size must
// be > 0 and overlap must be in [0, size).
func New(tok Tokenizer, size, overlap int) (*Chunker, error) {
	if tok == nil {
		return nil, fmt.Errorf("chunk: tokenizer is required: %w", core.ErrInvalidConfig)
	}
	if size <= 0 {
		return nil, fmt.Errorf("chunk: size must be > 0: %w", core.ErrInvalidConfig)
	}
	if overlap < 0 || overlap >= size {
		return nil, fmt.Errorf("chunk: overlap must be in [0, size): %w", core.ErrInvalidConfig)
	}
	return &Chunker{tok: tok, size: size, overlap: overlap}, nil
}

// Split returns the chunks of text for windows of at most size tokens advancing
// by size-overlap; it returns nil when the tokenizer yields no tokens.
func (c *Chunker) Split(text string) []string {
	toks := c.tok.Tokens(text)
	if len(toks) == 0 {
		return nil
	}
	runes := []rune(text)
	step := c.size - c.overlap

	var out []string
	for start := 0; start < len(toks); start += step {
		end := start + c.size
		if end > len(toks) {
			end = len(toks)
		}
		from := toks[start].Start
		to := toks[end-1].End
		// Tokenizer is a public interface, so its offsets can be wrong. Clamp
		// them into the text instead of slicing out of range.
		if from < 0 {
			from = 0
		}
		if from > len(runes) {
			from = len(runes)
		}
		if to > len(runes) {
			to = len(runes)
		}
		if to < from {
			to = from
		}
		out = append(out, string(runes[from:to]))
		if end == len(toks) {
			break
		}
	}
	return out
}

// WhitespaceTokenizer is a Tokenizer whose units are whitespace-separated words.
type WhitespaceTokenizer struct{}

// Tokens returns the whitespace-separated tokens with rune offsets, skipping
// leading and trailing whitespace.
func (WhitespaceTokenizer) Tokens(text string) []Token {
	runes := []rune(text)
	var toks []Token
	i := 0
	for i < len(runes) {
		for i < len(runes) && unicode.IsSpace(runes[i]) {
			i++
		}
		if i >= len(runes) {
			break
		}
		start := i
		for i < len(runes) && !unicode.IsSpace(runes[i]) {
			i++
		}
		toks = append(toks, Token{Start: start, End: i})
	}
	return toks
}
