package model

import "testing"

func TestClientsImplementContextWindow(t *testing.T) {
	openai, err := NewOpenAI(OpenAIConfig{BaseURL: "http://x", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	ollama, err := NewOllama(OllamaConfig{BaseURL: "http://x", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}

	for name, c := range map[string]Client{"openai": openai, "ollama": ollama} {
		cw, ok := c.(ContextWindow)
		if !ok {
			t.Fatalf("%s: client does not implement ContextWindow", name)
		}
		if got := cw.ContextTokens(); got != 0 {
			t.Fatalf("%s: ContextTokens() = %d, want 0 for an unset window", name, got)
		}
	}
}

func TestContextTokensReportsTheConfiguredWindow(t *testing.T) {
	openai, err := NewOpenAI(OpenAIConfig{BaseURL: "http://x", Model: "m", ContextTokens: 128000})
	if err != nil {
		t.Fatal(err)
	}
	if got := openai.ContextTokens(); got != 128000 {
		t.Fatalf("openai ContextTokens() = %d, want 128000", got)
	}

	ollama, err := NewOllama(OllamaConfig{BaseURL: "http://x", Model: "m", ContextTokens: 8192})
	if err != nil {
		t.Fatal(err)
	}
	if got := ollama.ContextTokens(); got != 8192 {
		t.Fatalf("ollama ContextTokens() = %d, want 8192", got)
	}
}

func TestContextTokensRejectsNegativeWindow(t *testing.T) {
	if _, err := NewOpenAI(OpenAIConfig{BaseURL: "http://x", Model: "m", ContextTokens: -1}); err == nil {
		t.Fatal("NewOpenAI accepted a negative ContextTokens")
	}
	if _, err := NewOllama(OllamaConfig{BaseURL: "http://x", Model: "m", ContextTokens: -1}); err == nil {
		t.Fatal("NewOllama accepted a negative ContextTokens")
	}
}
