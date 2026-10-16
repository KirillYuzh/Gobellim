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

type Client interface {
	Chat(ctx context.Context, req Request) (Response, error)
	Model() string
}

type ContextWindow interface {
	ContextTokens() int
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

type FinishReason string

const (
	FinishStop          FinishReason = "stop"
	FinishToolCalls     FinishReason = "tool_calls"
	FinishLength        FinishReason = "length"
	FinishContentFilter FinishReason = "content_filter"
	FinishOther         FinishReason = "other"
)

func ParseFinishReason(s string) FinishReason {
	switch FinishReason(s) {
	case FinishStop, FinishToolCalls, FinishLength, FinishContentFilter:
		return FinishReason(s)
	default:
		return FinishOther
	}
}

type SchemaMode int

const (
	SchemaNative SchemaMode = iota
	SchemaInline
	SchemaSystem
)

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

type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	Cost             float64
}

type Pricing struct {
	InputPerMTok  float64
	OutputPerMTok float64
}

func (p Pricing) Cost(u Usage) float64 {
	return float64(u.PromptTokens)/1e6*p.InputPerMTok + float64(u.CompletionTokens)/1e6*p.OutputPerMTok
}

type Response struct {
	Message      Message
	Usage        Usage
	Model        string
	FinishReason FinishReason
}
