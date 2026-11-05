package gobellim

import (
	"context"
	"encoding/json"
)

// Role identifies the author of a Message.
type Role string

const (
	// RoleSystem marks trusted instructions written by the application.
	RoleSystem Role = "system"
	// RoleUser marks caller-supplied input.
	RoleUser Role = "user"
	// RoleAssistant marks model output: untrusted.
	RoleAssistant Role = "assistant"
	// RoleTool marks a tool result, correlated by ToolCallID.
	RoleTool Role = "tool"
)

// ToolCall is a tool invocation requested by the model.
type ToolCall struct {
	ID   string
	Name string
	// Args is the raw JSON arguments. It is untrusted until decoded and gated.
	Args string
}

// Message is one conversation entry.
type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
	Name       string
}

// ToolSpec describes a tool offered to the model.
type ToolSpec struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

// FinishReason reports why the model stopped generating.
type FinishReason string

const (
	// FinishStop means the model completed its answer.
	FinishStop FinishReason = "stop"
	// FinishToolCalls means the model requested tool calls.
	FinishToolCalls FinishReason = "tool_calls"
	// FinishLength means the output hit the token limit.
	FinishLength FinishReason = "length"
	// FinishOther is the fallback for unknown reasons.
	FinishOther FinishReason = "other"
)

// Request is one chat request. It carries no labels: provenance lives in the
// runtime, not in the provider request.
type Request struct {
	Messages   []Message
	Tools      []ToolSpec
	JSONSchema json.RawMessage
	MaxTokens  int
}

// Usage reports token counts for one model call.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// Response is the model's reply.
type Response struct {
	Message      Message
	Usage        Usage
	Model        string
	FinishReason FinishReason
}

// Model is the chat port. The application supplies its own adapter.
type Model interface {
	Chat(ctx context.Context, req Request) (Response, error)
}
