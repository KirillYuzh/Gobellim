// Package model defines the chat primitives and the provider clients.
package model

import (
	"context"
	"encoding/json"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Client is an LLM client. There is intentionally no streaming.
type Client interface {
	Chat(ctx context.Context, req Request) (Response, error)
	Model() string
}

type ToolCall struct {
	ID   string
	Name string
	Args string
}

type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
	Name       string
}

type ToolSpec struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

type Request struct {
	Messages    []Message
	Tools       []ToolSpec
	Temperature *float64
	MaxTokens   int
	JSONSchema  json.RawMessage
}

type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

type Response struct {
	Message Message
	Usage   Usage
	Model   string
}
