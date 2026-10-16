package model

import "testing"

func TestPricingCost(t *testing.T) {
	p := Pricing{InputPerMTok: 3, OutputPerMTok: 15}
	if got := p.Cost(Usage{PromptTokens: 1_000_000}); got != 3 {
		t.Fatalf("input cost = %v, want 3", got)
	}
	if got := p.Cost(Usage{CompletionTokens: 1_000_000}); got != 15 {
		t.Fatalf("output cost = %v, want 15", got)
	}
	if got := p.Cost(Usage{PromptTokens: 1000, CompletionTokens: 500}); got < 0.0105-1e-12 || got > 0.0105+1e-12 {
		t.Fatalf("mixed cost = %v, want ~0.0105", got)
	}
	if got := (Pricing{}).Cost(Usage{PromptTokens: 100, CompletionTokens: 100}); got != 0 {
		t.Fatalf("zero pricing must cost 0, got %v", got)
	}
}
