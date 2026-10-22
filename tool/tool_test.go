package tool

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	internaltool "github.com/KirillYuzh/Gobellim/internal/tool"
)

// A tool is gated by its own permission unless the caller names another one.
func TestDefaultPermissionIsToolName(t *testing.T) {
	tl := New("get_weather", func(context.Context, struct{}) (string, error) { return "", nil })
	if tl.Permission() != "tool:get_weather" {
		t.Fatalf("permission = %q, want %q", tl.Permission(), "tool:get_weather")
	}
}

func TestPermOverridesTheDefaultPermission(t *testing.T) {
	tl := New("get_weather", func(context.Context, struct{}) (string, error) { return "", nil },
		Perm("weather:read"))
	if tl.Permission() != "weather:read" {
		t.Fatalf("permission = %q, want %q", tl.Permission(), "weather:read")
	}
}

// The options must reach the declaration: a dropped option is a lost policy.
func TestOptionsReachTheDeclaration(t *testing.T) {
	tl := New("get_weather", func(context.Context, struct{}) (string, error) { return "", nil },
		Desc("Get the weather"),
		Write(),
		Approve(),
	)
	if tl.Description() != "Get the weather" {
		t.Fatalf("description = %q", tl.Description())
	}
	if tl.Policy().Effect() != internaltool.EffectWrite {
		t.Fatalf("effect = %v, want write", tl.Policy().Effect())
	}
	if !tl.Policy().RequiresApproval() {
		t.Fatal("the approval policy was dropped")
	}
}

// Egress without a host allowlist is a declaration error: an egress tool must
// name its hosts or opt out explicitly.
func TestEgressNeedsHosts(t *testing.T) {
	tl := New("fetch", func(context.Context, struct{}) (string, error) { return "", nil },
		Egress())
	if tl.Err() == nil {
		t.Fatal("an egress tool without hosts must fail to build")
	}
	if !errors.Is(tl.Err(), internaltool.ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", tl.Err())
	}
}

func TestEgressWithHostsBuilds(t *testing.T) {
	tl := New("fetch", func(context.Context, struct{}) (string, error) { return "", nil },
		Egress("api.example.com"))
	if tl.Err() != nil {
		t.Fatalf("err = %v", tl.Err())
	}
	if tl.Policy().Effect() != internaltool.EffectEgress {
		t.Fatalf("effect = %v, want egress", tl.Policy().Effect())
	}
	hosts := tl.Policy().EgressHosts()
	if len(hosts) != 1 || hosts[0] != "api.example.com" {
		t.Fatalf("hosts = %v", hosts)
	}
}

// AnyHost is an explicit opt-out, and it still means egress.
func TestAnyHostIsEgress(t *testing.T) {
	tl := New("fetch", func(context.Context, struct{}) (string, error) { return "", nil },
		AnyHost())
	if tl.Err() != nil {
		t.Fatalf("err = %v", tl.Err())
	}
	if !tl.Policy().EgressAny() {
		t.Fatal("the any-host egress policy was dropped")
	}
	if tl.Policy().Effect() != internaltool.EffectEgress {
		t.Fatalf("effect = %v, want egress", tl.Policy().Effect())
	}
}

// A raw-schema tool hands its arguments to the handler as raw JSON, so an
// object schema works.
func TestRawArgumentsReachTheHandler(t *testing.T) {
	var got json.RawMessage
	tl := New[json.RawMessage, string]("raw", func(_ context.Context, in json.RawMessage) (string, error) {
		got = in
		return "ok", nil
	})
	out, err := tl.Invoke(context.Background(), json.RawMessage(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `"ok"` {
		t.Fatalf("out = %s", out)
	}
	if string(got) != `{"a":1}` {
		t.Fatalf("args = %s, want the raw object", got)
	}
}
