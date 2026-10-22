package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type in struct {
	Query string `json:"query"`
}

type out struct {
	Answer string `json:"answer"`
}

func echo(context.Context, in) (out, error) { return out{Answer: "ok"}, nil }

func TestToolDeclaresAndInvokes(t *testing.T) {
	tl := New("echo", echo, Desc("echo the query"), Perm("echo:use"), Write())
	if err := tl.Err(); err != nil {
		t.Fatalf("declare: %v", err)
	}
	if tl.Permission() != "echo:use" {
		t.Fatalf("permission = %q", tl.Permission())
	}
	if tl.Effect() != EffectWrite {
		t.Fatalf("effect = %v", tl.Effect())
	}
	if !strings.Contains(string(tl.Schema()), `"query"`) {
		t.Fatalf("schema = %s, want a query property", tl.Schema())
	}
	got, err := tl.Invoke(context.Background(), json.RawMessage(`{"query":"hi"}`))
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if got != `{"answer":"ok"}` {
		t.Fatalf("result = %s", got)
	}
}

func TestInvokeRejectsBadArguments(t *testing.T) {
	tl := New("echo", echo)
	cases := map[string]string{
		"wrong type":    `{"query":1}`,
		"unknown field": `{"query":"hi","extra":1}`,
		"not json":      `{`,
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := tl.Invoke(context.Background(), json.RawMessage(args)); err == nil {
				t.Fatalf("invoke(%s) succeeded", args)
			}
		})
	}
}

func TestDefaultPermissionIsToolName(t *testing.T) {
	if got := New("link", echo).Permission(); got != "tool:link" {
		t.Fatalf("permission = %q, want tool:link", got)
	}
}

func TestRegistryReportsDuplicateAndInvalidDeclarations(t *testing.T) {
	tl := New("echo", echo)
	if _, err := NewRegistry().Register(tl, tl).Build(); err == nil {
		t.Fatal("duplicate name must fail Build")
	}
	bad := New("", echo)
	if _, err := NewRegistry().Register(bad).Build(); err == nil {
		t.Fatal("empty name must fail Build")
	}
	nilHandler := New[in, out]("nil", nil)
	if _, err := NewRegistry().Register(nilHandler).Build(); err == nil {
		t.Fatal("nil handler must fail Build")
	}
}

func TestSetKeepsRegistrationOrder(t *testing.T) {
	set, err := NewRegistry().Register(
		New("b", echo),
		New("a", echo),
	).Build()
	if err != nil {
		t.Fatal(err)
	}
	all := set.All()
	if len(all) != 2 || all[0].Name() != "b" || all[1].Name() != "a" {
		t.Fatalf("All() = %v", all)
	}
	names := set.Names()
	if names[0] != "a" || names[1] != "b" {
		t.Fatalf("Names() = %v, want lexical order", names)
	}
}
