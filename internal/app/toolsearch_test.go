package app

import (
	"context"
	"fmt"
	"testing"

	"github.com/KirillYuzh/Gobellim/model"
)

const toolSearchPolicy = `
version: 1
default: deny
roles:
  admin:
    permissions:
      - chat
      - "*"
`

func echoTool(context.Context, struct{}) (echoOut, error) { return echoOut{Text: "ok"}, nil }

// echoTools declares n generic utility tools, appended to any extra defs.
func echoTools(n int, extra ...ToolDef) []ToolDef {
	defs := make([]ToolDef, 0, n+len(extra))
	for i := 0; i < n; i++ {
		defs = append(defs, Tool(fmt.Sprintf("util_%02d", i), echoTool, Desc("generic capability")))
	}
	return append(defs, extra...)
}

func TestRankToolsPicksTheBestMatchFirst(t *testing.T) {
	specs := []model.ToolSpec{
		{Name: "util_00", Description: "generic capability"},
		{Name: "weather_forecast", Description: "forecast the weather"},
		{Name: "util_01", Description: "generic capability"},
	}
	got := rankTools(specs, "what is the weather forecast", 2)
	if len(got) != 2 {
		t.Fatalf("got %d tools, want 2", len(got))
	}
	if got[0].Name != "weather_forecast" {
		t.Fatalf("tools = %v", names(got))
	}
}

func TestRankToolsKeepsEverythingWhenNothingMatches(t *testing.T) {
	specs := []model.ToolSpec{{Name: "alpha"}, {Name: "beta"}, {Name: "gamma"}}
	got := rankTools(specs, "zzz qqq", 1)
	if len(got) != 3 {
		t.Fatalf("got %d tools, want all 3", len(got))
	}
}

func TestSearchTokensIsUnicodeAware(t *testing.T) {
	got := searchTokens("Погода на завтра, 5 дней!")
	want := map[string]bool{"погода": true, "на": true, "завтра": true, "дней": true}
	if len(got) != len(want) {
		t.Fatalf("tokens = %v", got)
	}
	for _, tok := range got {
		if !want[tok] {
			t.Fatalf("unexpected token %q in %v", tok, got)
		}
	}
}

func TestToolSearchNarrowsTheExposedSet(t *testing.T) {
	llm := &scripted{t: t, queue: []model.Response{textResponse("ok")}}
	app := newRealApp(t, discardES{}, llm, nil, toolSearchPolicy, func(c *Config) {
		c.ToolSearch = ToolSearch{Enabled: true, Threshold: 5, TopK: 2}
	}, echoTools(24, Tool("weather_forecast", echoTool, Desc("forecast the weather")))...)

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})
	if _, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "what is the weather forecast"}}}); err != nil {
		t.Fatal(err)
	}
	if len(exposed(llm)) != 2 {
		t.Fatalf("exposed tools = %d, want 2", len(exposed(llm)))
	}
	if exposed(llm)[0].Name != "weather_forecast" {
		t.Fatalf("exposed tools = %v", names(exposed(llm)))
	}
}

func TestToolSearchOffExposesEveryAllowedTool(t *testing.T) {
	llm := &scripted{t: t, queue: []model.Response{textResponse("ok")}}
	app := newRealApp(t, discardES{}, llm, nil, toolSearchPolicy, nil, echoTools(9)...)

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})
	if _, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "weather"}}}); err != nil {
		t.Fatal(err)
	}
	if len(exposed(llm)) != 9 {
		t.Fatalf("exposed tools = %d, want 9", len(exposed(llm)))
	}
}

func TestToolSearchBelowThresholdIsNotApplied(t *testing.T) {
	llm := &scripted{t: t, queue: []model.Response{textResponse("ok")}}
	app := newRealApp(t, discardES{}, llm, nil, toolSearchPolicy, func(c *Config) {
		c.ToolSearch = ToolSearch{Enabled: true, Threshold: 100, TopK: 1}
	}, echoTools(3)...)

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})
	if _, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "util_01"}}}); err != nil {
		t.Fatal(err)
	}
	if len(exposed(llm)) != 3 {
		t.Fatalf("exposed tools = %d, want 3 (below threshold)", len(exposed(llm)))
	}
}

func names(specs []model.ToolSpec) []string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.Name)
	}
	return out
}

func exposed(llm *scripted) []model.ToolSpec {
	if len(llm.got) == 0 {
		return nil
	}
	return llm.got[len(llm.got)-1].Tools
}
