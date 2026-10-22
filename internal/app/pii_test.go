package app

import (
	"context"
	"strings"
	"testing"

	"github.com/KirillYuzh/Gobellim/guard"
	"github.com/KirillYuzh/Gobellim/model"
)

const piiPolicy = `
version: 1
default: deny
roles:
  user:
    permissions:
      - "*"
`

type phoneIn struct {
	Phone string `json:"phone"`
}

// piiTool declares a typed tool that records the argument it received, so a test
// can assert what the handler actually saw after PII rehydration.
func piiTool(t *testing.T, name string, allowPII bool, got *string) ToolDef {
	t.Helper()
	opts := make([]Option, 0, 1)
	if allowPII {
		opts = append(opts, Policy{}.AllowPII())
	}
	return Tool(name, func(_ context.Context, in phoneIn) (phoneIn, error) {
		*got = in.Phone
		return in, nil
	}, opts...)
}

func piiApp(t *testing.T, def ToolDef, subs []guard.Substitution) (*App, *turn) {
	t.Helper()
	app := newApp(t, Config{
		LLM: regionalLLM{region: model.RegionRU}, Policy: mustPolicy(t, piiPolicy), Spotlight: SpotlightOff,
	}, def)
	return app, &turn{
		traceID:      "t",
		principal:    Principal{UserID: "u", Roles: []string{"user"}, Tenant: "acme"},
		callsPerTool: map[string]int{},
		approved:     map[string]bool{},
		subs:         subs,
	}
}

func TestExecToolRehydratesForAllowPIITool(t *testing.T) {
	var got string
	def := piiTool(t, "send_sms", true, &got)
	subs := []guard.Substitution{{Placeholder: "[PII:phone]", Category: guard.CatPII, Value: "+7 999 888-77-66"}}
	app, tn := piiApp(t, def, subs)

	out, err := app.execTool(WithPrincipal(context.Background(), tn.principal),
		model.ToolCall{Name: "send_sms", Args: `{"phone":"[PII:phone]"}`}, tn)
	if err != nil {
		t.Fatal(err)
	}
	if got != "+7 999 888-77-66" {
		t.Fatalf("the tool did not receive the real value: %q", got)
	}
	if strings.Contains(out, "+7 999 888-77-66") {
		t.Fatalf("the real value leaked back to the model: %q", out)
	}
	if !strings.Contains(out, "[PII:phone]") {
		t.Fatalf("the result must stay masked: %q", out)
	}
}

func TestExecToolRefusesAmbiguousSubstitutions(t *testing.T) {
	var got string
	def := piiTool(t, "send_sms", true, &got)
	subs := []guard.Substitution{
		{Placeholder: "[PII:phone]", Category: guard.CatPII, Value: "+7 111"},
		{Placeholder: "[PII:phone]", Category: guard.CatPII, Value: "+7 222"},
	}
	app, tn := piiApp(t, def, subs)

	out, err := app.execTool(WithPrincipal(context.Background(), tn.principal),
		model.ToolCall{Name: "send_sms", Args: `{"phone":"[PII:phone]"}`}, tn)
	if err != nil {
		t.Fatal(err)
	}
	if got != "[PII:phone]" {
		t.Fatalf("an ambiguous placeholder must reach the tool unchanged, got %q", got)
	}
	if !strings.Contains(out, "[PII:phone]") {
		t.Fatalf("out = %q", out)
	}
}

func TestExecToolKeepsPlaceholdersWithoutAllowPII(t *testing.T) {
	var got string
	def := piiTool(t, "log_note", false, &got)
	subs := []guard.Substitution{{Placeholder: "[PII:phone]", Category: guard.CatPII, Value: "+7 999"}}
	app, tn := piiApp(t, def, subs)

	if _, err := app.execTool(WithPrincipal(context.Background(), tn.principal),
		model.ToolCall{Name: "log_note", Args: `{"phone":"[PII:phone]"}`}, tn); err != nil {
		t.Fatal(err)
	}
	if got != "[PII:phone]" {
		t.Fatalf("a tool without AllowPII must see the placeholder, got %q", got)
	}
}

func TestRehydrateArgsOnlyWhenUnambiguous(t *testing.T) {
	subs := []guard.Substitution{{Placeholder: "[PII:phone]", Category: guard.CatPII, Value: "+7 111"}}

	if got, ok := rehydrateArgs(`{"p":"[PII:phone]"}`, true, subs); !ok || got != `{"p":"+7 111"}` {
		t.Fatalf("got %q ok=%v", got, ok)
	}
	if _, ok := rehydrateArgs(`{"p":"[PII:phone]"}`, false, subs); ok {
		t.Fatal("AllowPII=false must not rehydrate")
	}
	ambiguous := append(append([]guard.Substitution(nil), subs...),
		guard.Substitution{Placeholder: "[PII:phone]", Category: guard.CatPII, Value: "+7 222"})
	if _, ok := rehydrateArgs(`{"p":"[PII:phone]"}`, true, ambiguous); ok {
		t.Fatal("an ambiguous mapping must not rehydrate")
	}
	empty := []guard.Substitution{{Placeholder: "[PII:phone]", Category: guard.CatPII}}
	if _, ok := rehydrateArgs(`{"p":"[PII:phone]"}`, true, empty); ok {
		t.Fatal("an empty value must not rehydrate")
	}
}
