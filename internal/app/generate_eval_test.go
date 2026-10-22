package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/KirillYuzh/Gobellim/model"
	"github.com/KirillYuzh/Gobellim/trace"
)

type reviewAnswer struct {
	Approved bool   `json:"approved"`
	Notes    string `json:"notes"`
}

type offSchemaLLM struct{ content string }

func (offSchemaLLM) Model() string { return "off-schema" }

func (l offSchemaLLM) Chat(context.Context, model.Request) (model.Response, error) {
	return model.Response{
		Message: model.Message{Role: model.RoleAssistant, Content: l.content},
		Usage:   model.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
	}, nil
}

func generateCtx() context.Context {
	return WithPrincipal(context.Background(), Principal{UserID: "u", Tenant: "t", Roles: []string{"admin"}})
}

func TestGenerateValidatesBeforeDecoding(t *testing.T) {
	cases := []struct {
		name        string
		answer      string
		wantDecoded bool
		wantErr     bool
	}{
		{"on schema", `{"approved":true,"notes":"ок"}`, true, false},
		{"extra field", `{"approved":true,"notes":"ок","confidence":0.9}`, false, true},
		{"missing field", `{"approved":true}`, false, true},
		{"wrong field type", `{"approved":"yes","notes":"ок"}`, false, true},
		{"fenced prose", "Конечно, заявка одобрена.", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := newRealApp(t, discardES{}, offSchemaLLM{content: tc.answer}, nil, testPolicyMemory, nil)

			got, err := Generate[reviewAnswer](generateCtx(), app, GenerateRequest[reviewAnswer]{
				Prompt: "проверь заявку",
			})
			if tc.wantErr {
				if !errors.Is(err, ErrProvider) {
					t.Fatalf("err = %v, want ErrProvider", err)
				}
				if got.Decoded {
					t.Fatal("an off-schema answer must not report Decoded")
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if !got.Decoded {
				t.Fatal("an on-schema answer must report Decoded")
			}
			if !got.Value.Approved || got.Value.Notes != "ок" {
				t.Fatalf("decoded value = %+v", got.Value)
			}
		})
	}
}

func TestGenerateDecodedFlagDistinguishesSchemaFailure(t *testing.T) {
	fallback := reviewAnswer{Approved: false, Notes: "не удалось разобрать ответ"}

	t.Run("warn", func(t *testing.T) {
		app := newRealApp(t, discardES{}, offSchemaLLM{content: `{"approved":true,"extra":1}`}, nil, testPolicyMemory, nil)
		got, err := Generate[reviewAnswer](generateCtx(), app, GenerateRequest[reviewAnswer]{
			Prompt:        "проверь заявку",
			ErrorStrategy: Warn,
		})
		if err != nil {
			t.Fatalf("Warn must not fail the call: %v", err)
		}
		if got.Decoded {
			t.Fatal("Decoded must stay false under Warn")
		}
		if !strings.Contains(got.Raw, `"extra":1`) {
			t.Fatalf("Raw must hold the offending answer: %q", got.Raw)
		}
		if got.Value.Notes != "" {
			t.Fatalf("Value must stay zero under Warn: %+v", got.Value)
		}
	})

	t.Run("fallback", func(t *testing.T) {
		app := newRealApp(t, discardES{}, offSchemaLLM{content: `{"approved":true,"extra":1}`}, nil, testPolicyMemory, nil)
		got, err := Generate[reviewAnswer](generateCtx(), app, GenerateRequest[reviewAnswer]{
			Prompt:        "проверь заявку",
			ErrorStrategy: Fallback,
			Fallback:      &fallback,
		})
		if err != nil {
			t.Fatalf("Fallback must not fail the call: %v", err)
		}
		if got.Decoded {
			t.Fatal("Decoded must stay false under Fallback")
		}
		if !got.UsedFallback || got.Value != fallback {
			t.Fatalf("Value = %+v, UsedFallback = %v", got.Value, got.UsedFallback)
		}
	})
}

func TestGenerateOffSchemaAnswerIsObservable(t *testing.T) {
	const answer = `{"approved":true,"notes":"ок","confidence":0.9}`

	provider := &recordingProvider{}
	app := newRealApp(t, discardES{}, offSchemaLLM{content: answer}, nil, testPolicyMemory, func(c *Config) {
		c.TracerProvider = provider
	})

	got, err := Generate[reviewAnswer](generateCtx(), app, GenerateRequest[reviewAnswer]{
		Prompt:        "проверь заявку",
		ErrorStrategy: Warn,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.Decoded {
		t.Fatal("off-schema answer reported Decoded")
	}

	operation := provider.named(t, "generate")
	if out, _ := operation.attrs[trace.AttrOutput].(string); !strings.Contains(out, `"confidence":0.9`) {
		t.Fatalf("the trace output must keep the offending answer: %q", out)
	}
	for _, s := range provider.recorded() {
		if s.attrs[trace.AttrKind] == KindGeneration {
			return
		}
	}
	t.Fatal("no GENERATION span recorded")
}

func TestGenerateSchemaForAnyAcceptsAnything(t *testing.T) {
	app := newRealApp(t, discardES{}, offSchemaLLM{content: `{"whatever":[1,2,3]}`}, nil, testPolicyMemory, nil)

	got, err := Generate[any](generateCtx(), app, GenerateRequest[any]{Prompt: "что угодно"})
	if err != nil {
		t.Fatalf("T = any must accept any JSON: %v", err)
	}
	if !got.Decoded {
		t.Fatal("Decoded must be true for T = any")
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(got.Raw), &m); err != nil {
		t.Fatalf("Raw is not the original answer: %v", err)
	}
}
