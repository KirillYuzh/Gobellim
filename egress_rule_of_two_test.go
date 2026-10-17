package kit

import (
	"context"
	"errors"
	"testing"

	"github.com/KirillYuzh/Gobellim/model"
)

func TestRuleOfTwoIsEnforcedByDefault(t *testing.T) {
	app := &App{cfg: Config{}}
	def := &ToolDef{name: "send", effect: EffectEgress, egressAny: true}

	if err := app.checkEgress(def, `{}`, &turn{}); err != nil {
		t.Fatalf("trusted turn: %v", err)
	}
	if err := app.checkEgress(def, `{}`, &turn{untrusted: true}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("untrusted turn err = %v, want ErrUnauthorized", err)
	}
}

func TestRuleOfTwoOptOut(t *testing.T) {
	app := &App{cfg: Config{Egress: EgressPolicy{AllowEgressAfterUntrusted: true}}}
	def := &ToolDef{name: "send", effect: EffectEgress, egressAny: true}

	if err := app.checkEgress(def, `{}`, &turn{untrusted: true}); err != nil {
		t.Fatalf("explicit opt-out still blocked egress: %v", err)
	}
}

func TestRuleOfTwoDoesNotAffectHostAllowlist(t *testing.T) {
	app := &App{cfg: Config{Egress: EgressPolicy{AllowEgressAfterUntrusted: true}}}
	def := &ToolDef{name: "fetch", effect: EffectEgress, egressHosts: []string{"api.example.com"}}

	if err := app.checkEgress(def, `{"url":"https://evil.example.net/x"}`, &turn{untrusted: true}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("host outside the allowlist err = %v, want ErrUnauthorized", err)
	}
}

func TestRuleOfTwoBlocksEgressInARealTurn(t *testing.T) {
	type fetchIn struct {
		URL string `json:"url"`
	}
	type sendIn struct {
		To string `json:"to"`
	}

	llm := &scripted{t: t, queue: []model.Response{
		toolCallResponse("fetch_page", `{"url":"https://api.example.com/doc"}`),
		toolCallResponse("send_mail", `{"to":"a@example.com"}`),
		textResponse("готово"),
	}}
	app := newRealApp(t, discardES{}, llm, nil, testPolicyMemory, func(c *Config) {
		c.MaxSteps = 5
		c.MaxCallsPerTool = 2
	})
	sent := false
	if _, err := Tool(app, "fetch_page",
		func(context.Context, fetchIn) (string, error) {
			return "страница зарегистрирована на user@example.com", nil
		},
		Effects(EffectRead)); err != nil {
		t.Fatal(err)
	}
	if _, err := Tool(app, "send_mail",
		func(context.Context, sendIn) (string, error) { sent = true; return "sent", nil },
		Effects(EffectEgress), EgressAny()); err != nil {
		t.Fatal(err)
	}

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Tenant: "t", Roles: []string{"admin"}})
	_, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "возьми страницу и отправь её по почте"}}})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if sent {
		t.Fatal("the egress handler must not run once the Rule of Two blocked the call")
	}
}

// ruleOfTwoTurn runs a fetch_page (EffectRead) then a send_mail (EffectEgress) in
// one Chat turn and reports the error and whether the egress handler ran.
func ruleOfTwoTurn(t *testing.T, fetchResult string, optOut bool) (bool, error) {
	t.Helper()
	type fetchIn struct {
		URL string `json:"url"`
	}
	type sendIn struct {
		To string `json:"to"`
	}

	llm := &scripted{t: t, queue: []model.Response{
		toolCallResponse("fetch_page", `{"url":"https://api.example.com/doc"}`),
		toolCallResponse("send_mail", `{"to":"a@example.com"}`),
		textResponse("готово"),
	}}
	app := newRealApp(t, discardES{}, llm, nil, testPolicyMemory, func(c *Config) {
		c.MaxSteps = 5
		c.MaxCallsPerTool = 2
		if optOut {
			c.Egress = EgressPolicy{AllowEgressAfterUntrusted: true}
		}
	})
	sent := false
	if _, err := Tool(app, "fetch_page",
		func(context.Context, fetchIn) (string, error) { return fetchResult, nil },
		Effects(EffectRead)); err != nil {
		t.Fatal(err)
	}
	if _, err := Tool(app, "send_mail",
		func(context.Context, sendIn) (string, error) { sent = true; return "sent", nil },
		Effects(EffectEgress), EgressAny()); err != nil {
		t.Fatal(err)
	}

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Tenant: "t", Roles: []string{"admin"}})
	_, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{
		{Role: model.RoleUser, Content: "возьми страницу и отправь её по почте"},
	}})
	return sent, err
}

// TestRuleOfTwoBlocksEgressAfterAnAllowedToolResult is the regression test for the
// hole session.md found: a tool result the guard *allowed* used to leave the turn
// trusted, so a later egress call slipped through. Origin-based taint (BR-101) must
// close it — the turn is untrusted regardless of the verdict.
func TestRuleOfTwoBlocksEgressAfterAnAllowedToolResult(t *testing.T) {
	sent, err := ruleOfTwoTurn(t, "страница без персональных данных", false)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if sent {
		t.Fatal("the egress handler must not run after any tool result")
	}
}

func TestRuleOfTwoOptOutAllowsEgressAfterAToolResult(t *testing.T) {
	sent, err := ruleOfTwoTurn(t, "страница без персональных данных", true)
	if err != nil {
		t.Fatalf("opt-out turn: %v", err)
	}
	if !sent {
		t.Fatal("AllowEgressAfterUntrusted must let the egress handler run")
	}
}

func TestRuleOfTwoAllowsEgressWithoutPriorToolResult(t *testing.T) {
	type sendIn struct {
		To string `json:"to"`
	}
	llm := &scripted{t: t, queue: []model.Response{
		toolCallResponse("send_mail", `{"to":"a@example.com"}`),
		textResponse("готово"),
	}}
	app := newRealApp(t, discardES{}, llm, nil, testPolicyMemory, func(c *Config) { c.MaxSteps = 3 })
	sent := false
	if _, err := Tool(app, "send_mail",
		func(context.Context, sendIn) (string, error) { sent = true; return "sent", nil },
		Effects(EffectEgress), EgressAny()); err != nil {
		t.Fatal(err)
	}

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Tenant: "t", Roles: []string{"admin"}})
	if _, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{
		{Role: model.RoleUser, Content: "отправь письмо"},
	}}); err != nil {
		t.Fatalf("trusted turn: %v", err)
	}
	if !sent {
		t.Fatal("egress without a prior tool result must be allowed")
	}
}

// TestEgressAllowlistSeesThroughJSONEscapes is the regression test for the hole
// where the allowlist read the raw argument text: a JSON-escaped URL
// (`https:\/\/host`, or `\u003a` for the colon) yielded no host, so the call was
// allowed. The arguments are decoded now, so the host is always visible.
func TestEgressAllowlistSeesThroughJSONEscapes(t *testing.T) {
	app := &App{cfg: Config{Egress: EgressPolicy{AllowEgressAfterUntrusted: true}}}
	def := &ToolDef{name: "fetch", effect: EffectEgress, egressHosts: []string{"api.example.com"}}

	for _, args := range []string{
		`{"url":"https:\/\/evil.example.net\/x"}`,
		`{"url":"https\u003a\/\/evil.example.net/x"}`,
		`{"url":"https:\/\/api.example.com\/ok","extra":["https:\/\/evil.example.net\/y"]}`,
	} {
		if err := app.checkEgress(def, args, &turn{}); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("args %s: err = %v, want ErrUnauthorized", args, err)
		}
	}

	if err := app.checkEgress(def, `{"url":"https:\/\/api.example.com\/doc"}`, &turn{}); err != nil {
		t.Fatalf("an allowed host must pass even escaped: %v", err)
	}
}
