package kit

import (
	"context"
	"errors"
	"testing"

	"github.com/gobellim/kit/model"
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

func TestRuleOfTwoAllowsEgressOnATrustedTurn(t *testing.T) {
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
			return "страница без персональных данных", nil
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
	if _, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{
		{Role: model.RoleUser, Content: "возьми страницу и отправь её по почте"},
	}}); err != nil {
		t.Fatalf("trusted turn: %v", err)
	}
	if !sent {
		t.Fatal("the egress handler must run on a trusted turn")
	}
}
