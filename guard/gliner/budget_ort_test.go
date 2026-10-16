//go:build ort

package gliner

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/daulet/tokenizers"

	"github.com/KirillYuzh/Gobellim/guard"
)

func TestWindowFitsTokenBudget(t *testing.T) {
	tk, err := tokenizers.FromFile(filepath.Join("..", "..", "models", DefaultModel, "tokenizer.json"))
	if err != nil {
		t.Skipf("tokenizer not present: %v", err)
	}
	defer func() { _ = tk.Close() }()

	base := "Сотрудник отдела логистики подготовил квартальный отчёт и передал его в бухгалтерию для сверки. "
	text := ""
	for len([]rune(text)) < guard.DefaultWindowChars {
		text += base
	}
	runes := []rune(text)[:guard.DefaultWindowChars]
	ids, _ := tk.Encode(string(runes), false)
	if len(ids) > DefaultMaxTokens {
		t.Fatalf("a %d-rune window needs %d tokens, over the %d-token budget: the window would be truncated",
			guard.DefaultWindowChars, len(ids), DefaultMaxTokens)
	}
	t.Logf("window %d runes -> %d tokens (budget %d)", guard.DefaultWindowChars, len(ids), DefaultMaxTokens)

	en := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 40)
	idsEN, _ := tk.Encode(string([]rune(en)[:guard.DefaultWindowChars]), false)
	if len(idsEN) > DefaultMaxTokens {
		t.Fatalf("an english %d-rune window needs %d tokens, over the %d-token budget",
			guard.DefaultWindowChars, len(idsEN), DefaultMaxTokens)
	}
	t.Logf("window %d runes -> %d tokens in english (budget %d)", guard.DefaultWindowChars, len(idsEN), DefaultMaxTokens)
}
