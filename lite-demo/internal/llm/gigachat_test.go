package llm

import (
	"testing"

	"some-project/internal/entity"
)

func TestToGCMessageMapsToolResult(t *testing.T) {
	m, err := toGCMessage(entity.Message{Role: "tool", Name: "link", Content: `{"display":"x"}`})
	if err != nil {
		t.Fatal(err)
	}
	if m.Role != "function" || m.Name != "link" {
		t.Fatalf("message = %+v", m)
	}
}

func TestToGCMessageKeepsOneFunctionCall(t *testing.T) {
	m, err := toGCMessage(entity.Message{
		Role:      "assistant",
		ToolCalls: []entity.ToolCall{{Name: "research", Args: map[string]any{"query": "go"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.FunctionCall == nil || m.FunctionCall.Name != "research" {
		t.Fatalf("function call = %+v", m.FunctionCall)
	}
}

func TestToGCMessageRejectsParallelCalls(t *testing.T) {
	_, err := toGCMessage(entity.Message{
		Role: "assistant",
		ToolCalls: []entity.ToolCall{
			{Name: "a"}, {Name: "b"},
		},
	})
	if err == nil {
		t.Fatal("parallel tool calls must fail")
	}
}

func TestNewUUIDShape(t *testing.T) {
	id := newUUID()
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		t.Fatalf("uuid = %q", id)
	}
}
