package kit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gobellim/kit/authz"
	"github.com/gobellim/kit/embed"
	"github.com/gobellim/kit/guard"
	"github.com/gobellim/kit/guard/guardtest"
	"github.com/gobellim/kit/guard/rules"
	"github.com/gobellim/kit/internal/call"
	"github.com/gobellim/kit/model"
	"github.com/gobellim/kit/store"
	"github.com/gobellim/kit/trace"
)

type memES struct {
	mu      sync.Mutex
	docs    []map[string]any
	working map[string]map[string]any
}

func newMemES() *memES { return &memES{working: map[string]map[string]any{}} }

func (f *memES) Do(_ context.Context, req store.Request) (int, []byte, error) {
	path := req.Path
	switch {
	case req.Method == http.MethodPost && strings.HasPrefix(path, "/"+store.IndexMemoryMessages+"/_search"):
		return f.search(req.Body)
	case req.Method == http.MethodPost && path == "/_bulk":
		return f.bulk(req.Body)
	case req.Method == http.MethodPost && strings.HasPrefix(path, "/"+store.IndexMemoryMessages+"/_delete_by_query"):
		return f.clear(req.Body)
	case strings.HasPrefix(path, "/"+store.IndexMemoryWorking+"/_doc/"):
		id := strings.TrimPrefix(path, "/"+store.IndexMemoryWorking+"/_doc/")
		switch req.Method {
		case http.MethodGet:
			return f.getWorking(id)
		case http.MethodPut:
			return f.putWorking(id, req.Body)
		}
	}
	return http.StatusOK, []byte(`{}`), nil
}

func (f *memES) bulk(body []byte) (int, []byte, error) {
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := 0; i+1 < len(lines); i += 2 {
		// UseNumber keeps int64 sequence numbers exact; float64 would collapse
		// adjacent values at UnixNano magnitude, which Elasticsearch does not.
		dec := json.NewDecoder(bytes.NewReader([]byte(lines[i+1])))
		dec.UseNumber()
		var doc map[string]any
		if err := dec.Decode(&doc); err != nil {
			return http.StatusBadRequest, []byte(`{}`), err
		}
		f.docs = append(f.docs, doc)
	}
	return http.StatusOK, []byte(`{"errors":false,"items":[]}`), nil
}

func (f *memES) search(body []byte) (int, []byte, error) {
	var q struct {
		Size  int `json:"size"`
		Query struct {
			Bool struct {
				Filter []map[string]any `json:"filter"`
			} `json:"bool"`
			Knn struct {
				Filter struct {
					Bool struct {
						Filter []map[string]any `json:"filter"`
					} `json:"bool"`
				} `json:"filter"`
			} `json:"knn"`
		} `json:"query"`
	}
	if err := json.Unmarshal(body, &q); err != nil {
		return http.StatusBadRequest, []byte(`{}`), err
	}
	recall := len(q.Query.Knn.Filter.Bool.Filter) > 0
	terms := termValues(q.Query.Bool.Filter)
	if recall {
		terms = termValues(q.Query.Knn.Filter.Bool.Filter)
	}

	f.mu.Lock()
	matched := make([]map[string]any, 0, len(f.docs))
	for _, d := range f.docs {
		if matchesTerms(d, terms) {
			matched = append(matched, d)
		}
	}
	f.mu.Unlock()

	if !recall {
		sort.SliceStable(matched, func(i, j int) bool {
			return seqOf(matched[i]) > seqOf(matched[j])
		})
	}
	size := q.Size
	if size <= 0 || size > len(matched) {
		size = len(matched)
	}
	matched = matched[:size]

	hits := make([]any, 0, len(matched))
	for _, d := range matched {
		hits = append(hits, map[string]any{
			"_score": 1.0,
			"_source": map[string]any{
				"role": d["role"], "content": d["content"], "untrusted": d["untrusted"], "created_at": d["created_at"],
			},
		})
	}
	out, _ := json.Marshal(map[string]any{"hits": map[string]any{"hits": hits}})
	return http.StatusOK, out, nil
}

func (f *memES) clear(body []byte) (int, []byte, error) {
	var q struct {
		Query struct {
			Bool struct {
				Filter []map[string]any `json:"filter"`
			} `json:"bool"`
		} `json:"query"`
	}
	if err := json.Unmarshal(body, &q); err != nil {
		return http.StatusBadRequest, []byte(`{}`), err
	}
	terms := termValues(q.Query.Bool.Filter)

	f.mu.Lock()
	defer f.mu.Unlock()
	kept := make([]map[string]any, 0, len(f.docs))
	deleted := 0
	for _, d := range f.docs {
		if matchesTerms(d, terms) {
			deleted++
			continue
		}
		kept = append(kept, d)
	}
	f.docs = kept
	out, _ := json.Marshal(map[string]any{"deleted": deleted, "failures": []any{}})
	return http.StatusOK, out, nil
}

func (f *memES) getWorking(id string) (int, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w, ok := f.working[id]
	if !ok {
		return http.StatusNotFound, []byte(`{"found":false}`), nil
	}
	out, _ := json.Marshal(map[string]any{"found": true, "_source": w})
	return http.StatusOK, out, nil
}

func (f *memES) putWorking(id string, body []byte) (int, []byte, error) {
	var w map[string]any
	if err := json.Unmarshal(body, &w); err != nil {
		return http.StatusBadRequest, []byte(`{}`), err
	}
	f.mu.Lock()
	f.working[id] = w
	f.mu.Unlock()
	return http.StatusCreated, []byte(`{"result":"created"}`), nil
}

func termValues(clauses []map[string]any) map[string]string {
	out := map[string]string{}
	for _, c := range clauses {
		term, ok := c["term"].(map[string]any)
		if !ok {
			continue
		}
		for k, v := range term {
			if s, ok := v.(string); ok {
				out[k] = s
			}
		}
	}
	return out
}

func matchesTerms(doc map[string]any, terms map[string]string) bool {
	for k, v := range terms {
		s, _ := doc[k].(string)
		if s != v {
			return false
		}
	}
	return true
}

func seqOf(doc map[string]any) int64 {
	switch v := doc["seq"].(type) {
	case json.Number:
		n, _ := v.Int64()
		return n
	case float64:
		return int64(v)
	default:
		return 0
	}
}

const testPolicyMemory = `
version: 1
default: deny
roles:
  admin:
    permissions:
      - "*"
`

func newMemoryApp(t *testing.T, es store.ES, llm model.Client, embedder embed.Embedder, policyYAML string) *App {
	t.Helper()
	return newMemoryAppWithGuard(t, es, llm, embedder, policyYAML, guardtest.AllowAll())
}

func newMemoryAppWithGuard(t *testing.T, es store.ES, llm model.Client, embedder embed.Embedder, policyYAML string, layers ...guard.Detector) *App {
	t.Helper()
	policy, err := authz.Parse(strings.NewReader(policyYAML))
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := guard.New(guard.Options{Layers: layers})
	if err != nil {
		t.Fatal(err)
	}
	writer, err := trace.NewWriter(trace.Options{ES: discardES{}})
	if err != nil {
		t.Fatal(err)
	}
	app := &App{
		cfg: Config{
			ES: es, LLM: llm, Embedder: embedder,
			MaxSteps: 2, MaxCallsPerTool: 3, ToolTimeout: time.Second, ArgsMaxBytes: 1024,
		},
		policy: policy,
		guard:  pipeline,
		writer: writer,
		tools:  map[string]*ToolDef{},
	}
	app.calls = call.Deps{Authz: policy, Guard: pipeline, Tracer: tracerAdapter{writer}, GuardVersion: pipeline.Version()}
	return app
}

func memoryCtx() context.Context {
	return WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})
}

func TestMemoryAppendAndLoadRoundTrip(t *testing.T) {
	app := newMemoryApp(t, newMemES(), &fakeLLM{}, nil, testPolicyMemory)
	defer func() { _ = app.Close(context.Background()) }()
	ctx := memoryCtx()

	if err := app.MemoryAppend(ctx, "th1",
		model.Message{Role: model.RoleUser, Content: "hi"},
		model.Message{Role: model.RoleAssistant, Content: "hello"},
	); err != nil {
		t.Fatal(err)
	}
	msgs, err := app.MemoryLoad(ctx, "th1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("msgs = %+v", msgs)
	}
	if msgs[0].Role != model.RoleUser || msgs[0].Content != "hi" {
		t.Fatalf("first message = %+v", msgs[0])
	}
	if msgs[1].Role != model.RoleAssistant || msgs[1].Content != "hello" {
		t.Fatalf("second message = %+v", msgs[1])
	}
}

func TestMemoryLoadReturnsChronologicalOrder(t *testing.T) {
	app := newMemoryApp(t, newMemES(), &fakeLLM{}, nil, testPolicyMemory)
	defer func() { _ = app.Close(context.Background()) }()
	ctx := memoryCtx()

	for _, content := range []string{"one", "two", "three"} {
		if err := app.MemoryAppend(ctx, "th1", model.Message{Role: model.RoleUser, Content: content}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	msgs, err := app.MemoryLoad(ctx, "th1", 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"one", "two", "three"}
	if len(msgs) != len(want) {
		t.Fatalf("msgs = %+v", msgs)
	}
	for i, w := range want {
		if msgs[i].Content != w {
			t.Fatalf("msgs[%d] = %q, want %q (%+v)", i, msgs[i].Content, w, msgs)
		}
	}
}

func TestMemoryRequiresPermission(t *testing.T) {
	const noMem = `
version: 1
default: deny
roles:
  reader:
    permissions:
      - chat
`
	app := newMemoryApp(t, newMemES(), &fakeLLM{}, nil, noMem)
	defer func() { _ = app.Close(context.Background()) }()
	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"reader"}, Tenant: "t"})

	if err := app.MemoryAppend(ctx, "th1", model.Message{Role: model.RoleUser, Content: "x"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("append err = %v, want ErrUnauthorized", err)
	}
	if _, err := app.MemoryLoad(ctx, "th1", 0); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("load err = %v, want ErrUnauthorized", err)
	}
}

func TestMemoryRequiresUserID(t *testing.T) {
	app := newMemoryApp(t, newMemES(), &fakeLLM{}, nil, testPolicyMemory)
	defer func() { _ = app.Close(context.Background()) }()
	ctx := WithPrincipal(context.Background(), Principal{Roles: []string{"admin"}, Tenant: "t"})

	if _, err := app.MemoryLoad(ctx, "th1", 0); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestMemoryRequiresThreadID(t *testing.T) {
	app := newMemoryApp(t, newMemES(), &fakeLLM{}, nil, testPolicyMemory)
	defer func() { _ = app.Close(context.Background()) }()
	ctx := memoryCtx()

	if _, err := app.MemoryLoad(ctx, "", 0); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
}

func TestMemoryClear(t *testing.T) {
	app := newMemoryApp(t, newMemES(), &fakeLLM{}, nil, testPolicyMemory)
	defer func() { _ = app.Close(context.Background()) }()
	ctx := memoryCtx()

	if err := app.MemoryAppend(ctx, "th1",
		model.Message{Role: model.RoleUser, Content: "a"},
		model.Message{Role: model.RoleAssistant, Content: "b"},
	); err != nil {
		t.Fatal(err)
	}
	deleted, err := app.MemoryClear(ctx, "th1")
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("deleted = %d, want 2", deleted)
	}
	msgs, err := app.MemoryLoad(ctx, "th1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("msgs after clear = %+v", msgs)
	}
}

func TestMemoryWorkingRoundTrip(t *testing.T) {
	app := newMemoryApp(t, newMemES(), &fakeLLM{}, nil, testPolicyMemory)
	defer func() { _ = app.Close(context.Background()) }()
	ctx := memoryCtx()

	empty, err := app.MemoryWorking(ctx, "th1")
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("empty working = %+v", empty)
	}

	if err := app.MemorySetWorking(ctx, "th1", map[string]any{"name": "Ann", "tier": "pro"}); err != nil {
		t.Fatal(err)
	}
	got, err := app.MemoryWorking(ctx, "th1")
	if err != nil {
		t.Fatal(err)
	}
	if got["name"] != "Ann" || got["tier"] != "pro" {
		t.Fatalf("working = %+v", got)
	}
}

func TestMemoryRecallRequiresEmbedder(t *testing.T) {
	app := newMemoryApp(t, newMemES(), &fakeLLM{}, nil, testPolicyMemory)
	defer func() { _ = app.Close(context.Background()) }()
	ctx := memoryCtx()

	if _, err := app.MemoryRecall(ctx, "query", 3); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
}

func TestMemoryIsScopedByUser(t *testing.T) {
	storeES := newMemES()
	app := newMemoryApp(t, storeES, &fakeLLM{}, nil, testPolicyMemory)
	defer func() { _ = app.Close(context.Background()) }()

	ctxAlice := WithPrincipal(context.Background(), Principal{UserID: "alice", Roles: []string{"admin"}, Tenant: "t"})
	ctxBob := WithPrincipal(context.Background(), Principal{UserID: "bob", Roles: []string{"admin"}, Tenant: "t"})

	if err := app.MemoryAppend(ctxAlice, "th1", model.Message{Role: model.RoleUser, Content: "alice-secret"}); err != nil {
		t.Fatal(err)
	}
	msgs, err := app.MemoryLoad(ctxBob, "th1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("bob must not read alice's memory: %+v", msgs)
	}
}

func TestChatWithThreadUsesAndPersistsMemory(t *testing.T) {
	llm := &scripted{t: t, queue: []model.Response{
		textResponse("first-answer"),
		textResponse("second-answer"),
	}}
	app := newMemoryApp(t, newMemES(), llm, nil, testPolicyMemory)
	defer func() { _ = app.Close(context.Background()) }()
	ctx := memoryCtx()

	if _, err := app.Chat(ctx, ChatRequest{
		ThreadID: "th1",
		Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Chat(ctx, ChatRequest{
		ThreadID: "th1",
		Messages: []model.Message{{Role: model.RoleUser, Content: "again"}},
	}); err != nil {
		t.Fatal(err)
	}

	if len(llm.got) != 2 {
		t.Fatalf("model calls = %d, want 2", len(llm.got))
	}
	var convo []model.Message
	for _, m := range llm.got[1].Messages {
		if m.Role != model.RoleSystem {
			convo = append(convo, m)
		}
	}
	if len(convo) != 3 {
		t.Fatalf("second call messages = %+v, want history + new user message", llm.got[1].Messages)
	}
	if convo[0].Role != model.RoleUser || convo[0].Content != "hello" {
		t.Fatalf("history not replayed: %+v", convo)
	}
	if convo[1].Role != model.RoleAssistant || convo[1].Content != "first-answer" {
		t.Fatalf("history not replayed: %+v", convo)
	}
	if convo[2].Content != "again" {
		t.Fatalf("new message missing: %+v", convo)
	}
}

func TestChatMarksAppendedMessagesUntrusted(t *testing.T) {
	es := newMemES()
	llm := &scripted{t: t, queue: []model.Response{textResponse("noted")}}
	app := newMemoryAppWithGuard(t, es, llm, nil, testPolicyMemory, rules.New())
	defer func() { _ = app.Close(context.Background()) }()

	// A valid SNILS is masked on the input rail, which marks the turn untrusted.
	if _, err := app.Chat(memoryCtx(), ChatRequest{
		ThreadID: "th1",
		Messages: []model.Message{{Role: model.RoleUser, Content: "snils 112-233-445 95"}},
	}); err != nil {
		t.Fatal(err)
	}
	if len(es.docs) == 0 {
		t.Fatal("nothing was written to memory")
	}
	for _, d := range es.docs {
		if d["untrusted"] != true {
			t.Fatalf("stored message not marked untrusted: %+v", d)
		}
	}
}

func TestUntrustedHistoryBlocksEgressInTheNextTurn(t *testing.T) {
	for _, tc := range []struct {
		name      string
		untrusted bool
		wantErr   bool
	}{
		{"tainted history", true, true},
		{"clean history", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			es := newMemES()
			es.docs = append(es.docs, map[string]any{
				"thread_id": "th1", "tenant": "t", "user_id": "u", "role": "assistant",
				"content": "earlier answer", "seq": float64(1),
				"untrusted": tc.untrusted, "created_at": time.Now().UTC().Format(time.RFC3339Nano),
			})
			llm := &scripted{t: t, queue: []model.Response{
				toolCallResponse("send", `{"url":"https://any.example.com"}`),
				textResponse("done"),
			}}
			app := newMemoryApp(t, es, llm, nil, testPolicyMemory)
			app.cfg.Egress = EgressPolicy{RuleOfTwo: true}
			defer func() { _ = app.Close(context.Background()) }()

			type sendIn struct {
				URL string `json:"url"`
			}
			if _, err := Tool(app, "send", func(context.Context, sendIn) (string, error) {
				return "sent", nil
			}, Effects(EffectEgress), EgressAny()); err != nil {
				t.Fatal(err)
			}

			_, err := app.Chat(memoryCtx(), ChatRequest{
				ThreadID: "th1",
				Messages: []model.Message{{Role: model.RoleUser, Content: "go"}},
			})
			if tc.wantErr && !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("err = %v, want ErrUnauthorized (Rule of Two across turns)", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("err = %v, want a clean thread to be allowed", err)
			}
		})
	}
}
