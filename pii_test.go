package kit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gobellim/kit/authz"
	"github.com/gobellim/kit/guard"
	"github.com/gobellim/kit/internal/call"
	"github.com/gobellim/kit/model"
)

type passthroughGuard struct{}

func (passthroughGuard) Check(_ context.Context, rail guard.Rail, text string) (guard.Verdict, error) {
	return guard.Verdict{Action: guard.Allow, Rail: rail, Text: text}, nil
}

type phoneIn struct {
	Phone string `json:"phone"`
}

// piiTool builds a tool whose handler records the phone it received and echoes it
// back, so a test can see both directions.
func piiTool(t *testing.T, name string, allowPII bool, got *string) *ToolDef {
	t.Helper()
	schema, err := schemaFor[phoneIn]()
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compileSchema(name, schema)
	if err != nil {
		t.Fatal(err)
	}
	return &ToolDef{
		name:       name,
		permission: authz.ToolPermission(name),
		effect:     EffectRead,
		allowPII:   allowPII,
		schema:     schema,
		compiled:   compiled,
		invoke: func(_ context.Context, args string) (string, error) {
			var in phoneIn
			if err := json.Unmarshal([]byte(args), &in); err != nil {
				return "", err
			}
			*got = in.Phone
			b, err := json.Marshal(phoneIn{Phone: in.Phone})
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	}
}

func piiApp(t *testing.T, def *ToolDef, subs []guard.Substitution) (*App, *turn) {
	t.Helper()
	policy, err := authz.Parse(strings.NewReader("version: 1\ndefault: deny\nroles:\n  user:\n    permissions:\n      - \"*\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	app := &App{
		cfg:    Config{ToolTimeout: time.Second, ArgsMaxBytes: 4096, MaxCallsPerTool: 3, Spotlight: SpotlightOff},
		policy: policy,
		tools:  map[string]*ToolDef{def.name: def},
		calls:  call.Deps{Guard: passthroughGuard{}},
	}
	return app, &turn{
		traceID:      "t",
		principal:    Principal{UserID: "u", Roles: []string{"user"}, Tenant: "acme"},
		callsPerTool: map[string]int{},
		invalidTries: map[string]int{},
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
