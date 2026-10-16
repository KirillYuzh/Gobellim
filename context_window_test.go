package kit

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/KirillYuzh/Gobellim/authz"
	"github.com/KirillYuzh/Gobellim/model"
)

func mustPolicy(t *testing.T, policyYAML string) *authz.Policy {
	t.Helper()
	policy, err := authz.Parse(strings.NewReader(policyYAML))
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func TestNewRejectsGuardWindowWiderThanAnUnsetModelWindow(t *testing.T) {
	policy := mustPolicy(t, testPolicy)
	cfg := newConfigWithWindow(0, 8000)
	cfg.Policy = policy

	app, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New with an unknown context window failed: %v", err)
	}
	if err := app.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestNewUsesTheDefaultGuardWindowWhenUnset(t *testing.T) {
	policy := mustPolicy(t, testPolicy)
	cfg := newConfigWithWindow(100, 0)
	cfg.Policy = policy

	_, err := New(context.Background(), cfg)
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), "context window") {
		t.Fatalf("error must name the context window: %v", err)
	}
}

func TestShippedClientsLetNewEnforceI06(t *testing.T) {
	const window = 4000
	openai, err := model.NewOpenAI(model.OpenAIConfig{BaseURL: "http://x", Model: "m", ContextTokens: window})
	if err != nil {
		t.Fatal(err)
	}
	ollama, err := model.NewOllama(model.OllamaConfig{BaseURL: "http://x", Model: "m", ContextTokens: window})
	if err != nil {
		t.Fatal(err)
	}
	gigachat, err := model.GigaChat(model.GigaChatConfig{Credentials: "id:secret", ContextTokens: window})
	if err != nil {
		t.Fatal(err)
	}

	for name, client := range map[string]model.Client{"openai": openai, "ollama": ollama, "gigachat": gigachat} {
		policy := mustPolicy(t, testPolicy)
		cfg := Config{ES: discardES{}, LLM: client, Policy: policy, GuardMaxChars: window + 1}
		if _, err := New(context.Background(), cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("%s: err = %v, want ErrInvalidConfig", name, err)
		}

		cfg = Config{ES: discardES{}, LLM: client, Policy: policy, GuardMaxChars: window - 1}
		app, err := New(context.Background(), cfg)
		if err != nil {
			t.Fatalf("%s: a guard window inside the model window must be accepted: %v", name, err)
		}
		if err := app.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func newConfigWithWindow(window, guardMaxChars int) Config {
	return Config{
		ES:            discardES{},
		LLM:           windowLLM{window: window},
		GuardMaxChars: guardMaxChars,
	}
}
