package agent

import "testing"

func TestAppendSuffixDropsModelLinks(t *testing.T) {
	got := appendSuffix("Документация здесь: [доки](https://go.dev/doc/)", []string{"https://example.invalid/go-doc (mock)"})
	want := "Документация здесь\n\nhttps://example.invalid/go-doc (mock)"
	if got != want {
		t.Fatalf("answer = %q, want %q", got, want)
	}
}

func TestAppendSuffixWithoutExtra(t *testing.T) {
	got := appendSuffix("см. https://go.dev/doc", nil)
	if got != "см. https://go.dev/doc" {
		t.Fatalf("answer = %q, want the text unchanged", got)
	}
}
