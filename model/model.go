package model

import (
	"context"
	"encoding/json"
)

// Role identifies the author of a Message.
type Role string

const (
	// RoleSystem marks instructions that precede the conversation.
	RoleSystem Role = "system"
	// RoleUser marks input supplied by the caller.
	RoleUser Role = "user"
	// RoleAssistant marks text or tool calls produced by the model.
	RoleAssistant Role = "assistant"
	// RoleTool marks the result of a tool call, correlated by ToolCallID.
	RoleTool Role = "tool"
)

// Client is the chat port: Chat sends one request and Model reports the
// provider's model name.
type Client interface {
	Chat(ctx context.Context, req Request) (Response, error)
	Model() string
}

// ContextWindow is an optional Client extension reporting the model's context
// size in tokens, which kit.New checks against the guard window.
type ContextWindow interface {
	ContextTokens() int
}

// ToolCall is a tool invocation requested by the model: its ID, the tool Name
// and the raw JSON Args.
type ToolCall struct {
	ID   string
	Name string
	Args string
}

// Message is one conversation entry; ToolCalls is set on assistant messages and
// ToolCallID/Name on tool results.
type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
	Name       string
}

// ToolSpec describes a tool offered to the model; Schema is a JSON Schema.
type ToolSpec struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

// FinishReason reports why the model stopped generating.
type FinishReason string

const (
	// FinishStop means the model completed its answer normally.
	FinishStop FinishReason = "stop"
	// FinishToolCalls means the model stopped to request tool calls.
	FinishToolCalls FinishReason = "tool_calls"
	// FinishLength means the output hit the token limit.
	FinishLength FinishReason = "length"
	// FinishContentFilter means the provider filtered the output.
	FinishContentFilter FinishReason = "content_filter"
	// FinishOther is the fallback for unknown or missing reasons.
	FinishOther FinishReason = "other"
)

// ParseFinishReason maps a provider string to a known FinishReason and returns
// FinishOther for anything unrecognised.
func ParseFinishReason(s string) FinishReason {
	switch FinishReason(s) {
	case FinishStop, FinishToolCalls, FinishLength, FinishContentFilter:
		return FinishReason(s)
	default:
		return FinishOther
	}
}

// SchemaMode selects how a Request's JSONSchema is conveyed to the provider.
type SchemaMode int

const (
	// SchemaNative asks the provider to enforce the schema natively
	// (response_format or format).
	SchemaNative SchemaMode = iota
	// SchemaInline appends the schema instructions to the last user message.
	SchemaInline
	// SchemaSystem appends the schema instructions to the system message.
	SchemaSystem
)

// Request is one chat request: messages, optional tools, sampling knobs and
// schema settings.
type Request struct {
	Messages        []Message
	Tools           []ToolSpec
	Temperature     *float64
	MaxTokens       int
	JSONSchema      json.RawMessage
	SchemaMode      SchemaMode
	ToolChoice      string
	Seed            *int
	Stop            []string
	ProviderOptions map[string]any
}

// Usage reports token counts and cost for one call.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	Cost             float64
}

// Pricing holds per-million-token input and output prices.
type Pricing struct {
	InputPerMTok  float64
	OutputPerMTok float64
}

// Cost returns the price of u in the pricing's currency units.
func (p Pricing) Cost(u Usage) float64 {
	return float64(u.PromptTokens)/1e6*p.InputPerMTok + float64(u.CompletionTokens)/1e6*p.OutputPerMTok
}

// Response is the model's reply together with usage and the provider's model
// name.
type Response struct {
	Message      Message
	Usage        Usage
	Model        string
	FinishReason FinishReason
}
