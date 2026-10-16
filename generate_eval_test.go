package kit

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/gobellim/kit/eval"
	"github.com/gobellim/kit/model"
	"github.com/gobellim/kit/trace"
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

func TestGenerateStructuringModelCanRepairAnOffSchemaAnswer(t *testing.T) {
	app := newRealApp(t, discardES{}, offSchemaLLM{content: "заявка одобрена, всё в порядке"},
		nil, testPolicyMemory, nil)

	got, err := Generate[reviewAnswer](generateCtx(), app, GenerateRequest[reviewAnswer]{
		Prompt:           "проверь заявку",
		StructuringModel: offSchemaLLM{content: `{"approved":true,"notes":"одобрена"}`},
		ErrorStrategy:    Warn,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !got.Decoded {
		t.Fatalf("a repaired answer must report Decoded: %+v", got)
	}
	if !got.Value.Approved {
		t.Fatalf("decoded value = %+v", got.Value)
	}
}

func TestGenerateOffSchemaAnswerAsEvalCase(t *testing.T) {
	const answer = `{"approved":true,"notes":"ок","confidence":0.9}`

	rec := trace.NewRecorder()
	app := newRealApp(t, discardES{}, offSchemaLLM{content: answer}, nil, testPolicyMemory, func(c *Config) {
		c.Sinks = []trace.Sink{rec}
	})
	rec.Reset()

	cases := []eval.Case{{
		Name:  "generate off-schema answer is rejected before decoding",
		Input: "проверь заявку",
		Checks: []eval.Check{
			eval.Matches(mustCompile(`"confidence":0\.9`)),
			eval.All(
				func(tr eval.Trace) error {
					for _, o := range tr.Observations {
						if o.Kind == trace.KindGeneration {
							return nil
						}
					}
					return errors.New("no GENERATION observation recorded")
				},
			),
		},
	}}

	results := eval.Run(generateCtx(), rec, cases, func(ctx context.Context, input string) error {
		var got Generated[reviewAnswer]
		_, err := Generate[reviewAnswer](ctx, app, GenerateRequest[reviewAnswer]{
			Prompt:        input,
			ErrorStrategy: Warn,
		})
		if err != nil {
			return err
		}
		if got.Decoded {
			return errors.New("off-schema answer reported Decoded")
		}
		return nil
	})

	report := eval.Summarize(results)
	if !report.OK() {
		t.Fatalf("eval case failed:\n%s", report.String())
	}
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

func mustCompile(expr string) *regexp.Regexp {
	re, err := regexp.Compile(expr)
	if err != nil {
		panic(err)
	}
	return re
}
