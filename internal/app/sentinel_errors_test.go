package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestCallToolReturnsErrToolNotFound(t *testing.T) {
	app := newRealApp(t, discardES{}, &fakeLLM{}, nil, testPolicyMemory, nil)

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Tenant: "t", Roles: []string{"admin"}})
	_, err := app.CallTool(ctx, "no_such_tool", nil)
	if !errors.Is(err, ErrToolNotFound) {
		t.Fatalf("err = %v, want ErrToolNotFound", err)
	}
	if !strings.Contains(err.Error(), "no_such_tool") {
		t.Fatalf("error must name the tool: %v", err)
	}
}

func TestCallToolSucceedsForAKnownTool(t *testing.T) {
	type echoIn struct {
		Text string `json:"text"`
	}
	app := newRealApp(t, discardES{}, &fakeLLM{}, nil, testPolicyMemory, nil,
		Tool("echo", func(_ context.Context, in echoIn) (string, error) {
			return "echo: " + in.Text, nil
		}),
	)

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Tenant: "t", Roles: []string{"admin"}})
	got, err := app.CallTool(ctx, "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatalf("err = %v, want a result", err)
	}
	if !strings.Contains(got, "hi") {
		t.Fatalf("result = %q", got)
	}
}

func TestToolHandlerFailureCarriesErrTool(t *testing.T) {
	type boomIn struct {
		Reason string `json:"reason"`
	}
	var seen error
	mw := func(ctx context.Context, _ Call, next Next) (json.RawMessage, error) {
		res, err := next(ctx)
		seen = err
		return res, err
	}
	app := newRealApp(t, discardES{}, &fakeLLM{}, nil, testPolicyMemory, func(c *Config) {
		c.Middleware = []Middleware{mw}
	}, Tool("boom", func(context.Context, boomIn) (string, error) {
		return "", errors.New("upstream timeout")
	}))

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Tenant: "t", Roles: []string{"admin"}})
	res, err := app.CallTool(ctx, "boom", json.RawMessage(`{"reason":"x"}`))
	if err != nil {
		t.Fatalf("a tool failure is data, not a turn failure: %v", err)
	}
	if !strings.Contains(res, "upstream timeout") {
		t.Fatalf("the model must see the tool's own message: %s", res)
	}
	if strings.Contains(res, ErrTool.Error()) {
		t.Fatalf("the sentinel label must not leak into the model's context: %s", res)
	}
	if !errors.Is(seen, ErrTool) {
		t.Fatalf("middleware error = %v, want ErrTool", seen)
	}
}

func TestFatalToolErrorsAreNotMarkedAsToolFailures(t *testing.T) {
	type boomIn struct {
		Reason string `json:"reason"`
	}
	app := newRealApp(t, discardES{}, &fakeLLM{}, nil, testPolicyMemory, nil,
		Tool("boom", func(context.Context, boomIn) (string, error) {
			return "", ErrProvider
		}),
	)

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Tenant: "t", Roles: []string{"admin"}})
	_, err := app.CallTool(ctx, "boom", json.RawMessage(`{"reason":"x"}`))
	if !errors.Is(err, ErrProvider) {
		t.Fatalf("err = %v, want ErrProvider", err)
	}
	if errors.Is(err, ErrTool) {
		t.Fatalf("a fatal error must not be relabelled: %v", err)
	}
}
