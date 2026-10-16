package chunk

import (
	"fmt"
	"unicode"

	"github.com/gobellim/kit/internal/core"
)

const (
	DefaultSize    = 400
	DefaultOverlap = 80
)

type Token struct {
	Text  string
	Start int
	End   int
}

type Tokenizer interface {
	Tokens(text string) []Token
}

type Chunker struct {
	tok     Tokenizer
	size    int
	overlap int
}

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
		if from < 0 {
			from = 0
		}
		if to > len(runes) {
			to = len(runes)
		}
		out = append(out, string(runes[from:to]))
		if end == len(toks) {
			break
		}
	}
	return out
}

type WhitespaceTokenizer struct{}

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
		toks = append(toks, Token{Text: string(runes[start:i]), Start: start, End: i})
	}
	return toks
}
