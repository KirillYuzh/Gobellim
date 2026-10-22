package kittest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/KirillYuzh/Gobellim/guard"
	"github.com/KirillYuzh/Gobellim/guard/guardtest"
	"github.com/KirillYuzh/Gobellim/model"
)

type memoryIndex struct {
	mu   sync.Mutex
	docs []Document
}

func (m *memoryIndex) Index(_ context.Context, doc Document) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.docs = append(m.docs, doc)
	return nil
}

func (m *memoryIndex) Search(_ context.Context, scope Scope, query string) ([]Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Document
	for _, doc := range m.docs {
		if doc.Tenant == "" || doc.Tenant != scope.Tenant || !containsAny(doc.ACL, scope.Roles) ||
			!strings.Contains(doc.Text, query) {
			continue
		}
		out = append(out, doc)
	}
	return out, nil
}

func containsAny(acl, roles []string) bool {
	for _, r := range roles {
		if slices.Contains(acl, r) {
			return true
		}
	}
	return false
}

func TestScriptedReplaysInOrder(t *testing.T) {
	llm := (&Scripted{}).Text("first").Call("c1", "get_weather", `{"city":"Moscow"}`)
	if llm.Left() != 2 {
		t.Fatalf("Left() = %d, want 2", llm.Left())
	}

	first, err := llm.Chat(context.Background(), model.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Message.Content != "first" {
		t.Fatalf("content = %q", first.Message.Content)
	}
	second, err := llm.Chat(context.Background(), model.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Message.ToolCalls) != 1 || second.Message.ToolCalls[0].Name != "get_weather" {
		t.Fatalf("tool calls = %+v", second.Message.ToolCalls)
	}
	if len(llm.Requests()) != 2 {
		t.Fatalf("Requests() = %d, want 2", len(llm.Requests()))
	}
	if _, err := llm.Chat(context.Background(), model.Request{}); err == nil {
		t.Fatal("an exhausted queue must return an error")
	}
}

func TestScriptedFail(t *testing.T) {
	sentinel := errors.New("provider down")
	llm := (&Scripted{}).Fail(sentinel)
	if _, err := llm.Chat(context.Background(), model.Request{}); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the configured failure", err)
	}
}

func TestScriptedCapabilities(t *testing.T) {
	var _ model.Client = NewContextWindowed(1)
	var _ model.Client = NewRegional(model.RegionRU)

	var cw model.ContextWindow = NewContextWindowed(4096)
	if cw.ContextTokens() != 4096 {
		t.Fatalf("ContextTokens() = %d", cw.ContextTokens())
	}
	var regional model.Regional = NewRegional(model.RegionEU)
	if regional.Region() != model.RegionEU {
		t.Fatalf("Region() = %q", regional.Region())
	}
}

func TestDetectorHelpers(t *testing.T) {
	ctx := context.Background()
	found := []guard.Finding{{Category: guard.CatPII, Label: "phone", Start: 0, End: 11}}

	always := Findings("L2", found...)
	if got, err := always.Detect(ctx, "anything"); err != nil || len(got) != 1 {
		t.Fatalf("Findings: %+v, %v", got, err)
	}

	conditional := Match("L2", "secret", found...)
	if got, _ := conditional.Detect(ctx, "no trigger"); len(got) != 0 {
		t.Fatalf("Match without the trigger: %+v", got)
	}
	if got, _ := conditional.Detect(ctx, "a secret here"); len(got) != 1 {
		t.Fatalf("Match with the trigger: %+v", got)
	}

	offline := errors.New("offline")
	if _, err := Unavailable("L2", offline).Detect(ctx, "text"); !errors.Is(err, offline) {
		t.Fatalf("Unavailable: err = %v", err)
	}

	if got, err := (Detector{Label: "L2"}).Detect(ctx, "text"); err != nil || got != nil {
		t.Fatalf("a nil DetectFunc must return no findings: %+v, %v", got, err)
	}
}

func TestContractsAcceptTheFakes(t *testing.T) {
	DetectorContract(t, guardtest.AllowAll())
	DetectorContract(t, Findings("L1"))
	ScopeIsolation(t, &memoryIndex{})
}

func TestReferenceIndexDeniesCrossScope(t *testing.T) {
	idx := &memoryIndex{}
	ctx := context.Background()
	for _, doc := range []Document{
		{ID: "a1", Tenant: "alpha", ACL: []string{"reader"}, Text: "alpha handbook"},
		{ID: "b1", Tenant: "beta", ACL: []string{"reader"}, Text: "beta handbook"},
	} {
		if err := idx.Index(ctx, doc); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct {
		name  string
		scope Scope
		want  []string
	}{
		{"own tenant", Scope{Tenant: "alpha", Roles: []string{"reader"}}, []string{"a1"}},
		{"other tenant", Scope{Tenant: "beta", Roles: []string{"reader"}}, []string{"b1"}},
		{"role outside the acl", Scope{Tenant: "alpha", Roles: []string{"outsider"}}, nil},
		{"empty scope", Scope{}, nil},
	} {
		got, err := idx.Search(ctx, tc.scope, "handbook")
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !slices.Equal(ids(got), tc.want) {
			t.Fatalf("%s: ids = %v, want %v", tc.name, ids(got), tc.want)
		}
	}
}
