package testkit_test

import (
	"context"
	"sync"
	"testing"

	gobellim "github.com/KirillYuzh/Gobellim"
	"github.com/KirillYuzh/Gobellim/testkit"
)

// A testkit is only useful if the fake is stricter than the code under test.
// These tests pin the three ways it could lie: play responses out of order,
// lose the record of what it saw, or hand back a request the caller can mutate.
func TestFakeModelPlaysInOrderAndRecordsACopy(t *testing.T) {
	model := testkit.NewFakeModel(
		testkit.Call("lookup", map[string]string{"q": "x"}),
		testkit.Reply(`{"ok":true}`),
	)
	req := gobellim.Request{
		Messages: []gobellim.Message{{
			Role:      gobellim.RoleAssistant,
			ToolCalls: []gobellim.ToolCall{{ID: "c1", Name: "lookup"}},
		}},
		Tools: []gobellim.ToolSpec{{Name: "lookup"}},
	}

	first, err := model.Chat(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Message.ToolCalls) != 1 || first.Message.ToolCalls[0].Name != "lookup" {
		t.Fatalf("first response = %+v, want the scripted call", first)
	}
	if _, err := model.Chat(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if got := model.Remaining(); got != 0 {
		t.Fatalf("Remaining = %d, want 0", got)
	}
	if _, err := model.Chat(context.Background(), req); err == nil {
		t.Fatal("an exhausted model returned a response instead of an error")
	}

	// The caller keeps the request it passed in and the fake keeps its own copy:
	// a later append in the task loop must not rewrite history.
	req.Messages[0].ToolCalls[0].Name = "mutated"
	req.Messages = append(req.Messages, gobellim.Message{Role: gobellim.RoleUser})
	calls := model.Calls()
	if len(calls) != 3 {
		t.Fatalf("calls = %d, want 3", len(calls))
	}
	if got := calls[0].Messages[0].ToolCalls[0].Name; got != "lookup" {
		t.Fatalf("recorded tool call = %q, want the value sent at call time", got)
	}
	if len(calls[0].Messages) != 1 {
		t.Fatalf("recorded messages = %d, want 1", len(calls[0].Messages))
	}
}

func TestRecorderIsSafeUnderConcurrency(t *testing.T) {
	rec := &testkit.Recorder{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec.Emit(context.Background(), gobellim.Event{Kind: gobellim.EventTaskCompleted})
			_ = rec.Count(gobellim.EventTaskCompleted)
			_ = rec.Events()
		}()
	}
	wg.Wait()
	if got := rec.Count(gobellim.EventTaskCompleted); got != 8 {
		t.Fatalf("Count = %d, want 8", got)
	}
	if got := len(rec.Events()); got != 8 {
		t.Fatalf("Events = %d, want 8", got)
	}
}

func TestJSONFallsBackToNull(t *testing.T) {
	if got := testkit.JSON(map[string]string{"a": "b"}); got != `{"a":"b"}` {
		t.Fatalf("JSON = %s", got)
	}
	// A channel cannot be marshalled. A scripted call with "null" arguments is a
	// bad script, but it must not panic inside a test helper.
	if got := testkit.JSON(make(chan int)); got != "null" {
		t.Fatalf("JSON(chan) = %s, want null", got)
	}
}
