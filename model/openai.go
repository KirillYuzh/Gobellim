package model

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

// OpenAIConfig configures an OpenAI-compatible client.
type OpenAIConfig struct {
	BaseURL    string
	APIKey     string
	Model      string
	Headers    map[string]string
	HTTPClient *http.Client

	ContextTokens int
}

// OpenAIClient is a Client speaking the OpenAI chat-completions protocol; it
// also implements Streamer.
type OpenAIClient struct {
	cfg OpenAIConfig
	hc  *http.Client
}

// NewOpenAI builds an OpenAIClient; BaseURL and Model are required and
// ContextTokens must not be negative, otherwise the error wraps
// core.ErrInvalidConfig.
func NewOpenAI(cfg OpenAIConfig) (*OpenAIClient, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("model: openai: BaseURL is required: %w", core.ErrInvalidConfig)
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("model: openai: Model is required: %w", core.ErrInvalidConfig)
	}
	if cfg.ContextTokens < 0 {
		return nil, fmt.Errorf("model: openai: ContextTokens must not be negative, got %d: %w", cfg.ContextTokens, core.ErrInvalidConfig)
	}
	return &OpenAIClient{cfg: cfg, hc: httpClient(cfg.HTTPClient)}, nil
}

// OpenAI is a shortcut for NewOpenAI pointed at api.openai.com with the given
// API key.
func OpenAI(apiKey, modelName string) (*OpenAIClient, error) {
	return NewOpenAI(OpenAIConfig{BaseURL: "https://api.openai.com/v1", APIKey: apiKey, Model: modelName})
}

// VLLM is a shortcut for NewOpenAI pointed at an OpenAI-compatible base URL
// with no API key.
func VLLM(baseURL, modelName string) (*OpenAIClient, error) {
	return NewOpenAI(OpenAIConfig{BaseURL: baseURL, Model: modelName})
}

// Model returns the configured model name.
func (c *OpenAIClient) Model() string { return c.cfg.Model }

type oaiMessage struct {
	Role       string       `json:"role"`
	Content    string       `json:"content,omitempty"`
	ToolCalls  []oaiToolReq `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
	Name       string       `json:"name,omitempty"`
}

type oaiToolReq struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaiToolSpec struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type oaiRequest struct {
	Model          string           `json:"model"`
	Messages       []oaiMessage     `json:"messages"`
	Tools          []oaiToolSpec    `json:"tools,omitempty"`
	ToolChoice     any              `json:"tool_choice,omitempty"`
	Temperature    *float64         `json:"temperature,omitempty"`
	MaxTokens      int              `json:"max_tokens,omitempty"`
	Seed           *int             `json:"seed,omitempty"`
	Stop           []string         `json:"stop,omitempty"`
	ResponseFormat *oaiRespFormat   `json:"response_format,omitempty"`
	Stream         bool             `json:"stream"`
	StreamOptions  *oaiStreamOption `json:"stream_options,omitempty"`
}

type oaiStreamOption struct {
	IncludeUsage bool `json:"include_usage"`
}

type oaiRespFormat struct {
	Type       string `json:"type"`
	JSONSchema struct {
		Name   string          `json:"name"`
		Schema json.RawMessage `json:"schema"`
	} `json:"json_schema"`
}

type oaiResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Role      string       `json:"role"`
			Content   string       `json:"content"`
			ToolCalls []oaiToolReq `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func oaiToolChoice(choice string) any {
	switch choice {
	case "":
		return nil
	case "auto", "none", "required":
		return choice
	default:
		return map[string]any{"type": "function", "function": map[string]string{"name": choice}}
	}
}

func (c *OpenAIClient) buildBody(req Request, stream bool) oaiRequest {
	msgs, native := schemaPlan(req)

	body := oaiRequest{
		Model:       c.cfg.Model,
		Messages:    make([]oaiMessage, 0, len(msgs)),
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Seed:        req.Seed,
		Stop:        req.Stop,
		ToolChoice:  oaiToolChoice(req.ToolChoice),
		Stream:      stream,
	}
	if stream {
		body.StreamOptions = &oaiStreamOption{IncludeUsage: true}
	}
	for _, m := range msgs {
		om := oaiMessage{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID, Name: m.Name}
		for _, tc := range m.ToolCalls {
			var ot oaiToolReq
			ot.ID = tc.ID
			ot.Type = "function"
			ot.Function.Name = tc.Name
			ot.Function.Arguments = tc.Args
			om.ToolCalls = append(om.ToolCalls, ot)
		}
		body.Messages = append(body.Messages, om)
	}
	for _, t := range req.Tools {
		var spec oaiToolSpec
		spec.Type = "function"
		spec.Function.Name = t.Name
		spec.Function.Description = t.Description
		spec.Function.Parameters = t.Schema
		body.Tools = append(body.Tools, spec)
	}
	if native {
		rf := &oaiRespFormat{Type: "json_schema"}
		rf.JSONSchema.Name = "output"
		rf.JSONSchema.Schema = req.JSONSchema
		body.ResponseFormat = rf
	}
	return body
}

func (c *OpenAIClient) headers() map[string]string {
	headers := map[string]string{}
	if c.cfg.APIKey != "" {
		headers["Authorization"] = "Bearer " + c.cfg.APIKey
	}
	for k, v := range c.cfg.Headers {
		headers[k] = v
	}
	return headers
}

func (c *OpenAIClient) encode(req Request, stream bool) (map[string]any, map[string]string, error) {
	payload, err := applyProviderOptions(req, c.buildBody(req, stream))
	if err != nil {
		return nil, nil, err
	}
	return payload, c.headers(), nil
}

// Chat performs one non-streaming request and returns the first choice; an
// empty choices list yields an error wrapping core.ErrProvider.
func (c *OpenAIClient) Chat(ctx context.Context, req Request) (Response, error) {
	outBody, headers, err := c.encode(req, false)
	if err != nil {
		return Response{}, err
	}

	var out oaiResponse
	if err := doJSON(ctx, c.hc, http.MethodPost, joinURL(c.cfg.BaseURL, "/chat/completions"), headers, outBody, &out); err != nil {
		return Response{}, err
	}
	if len(out.Choices) == 0 {
		return Response{}, fmt.Errorf("model: openai: empty choices: %w", core.ErrProvider)
	}
	msg := Message{Role: RoleAssistant, Content: out.Choices[0].Message.Content}
	for _, tc := range out.Choices[0].Message.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Args: tc.Function.Arguments})
	}
	return Response{
		Message:      msg,
		Model:        out.Model,
		Usage:        Usage{PromptTokens: out.Usage.PromptTokens, CompletionTokens: out.Usage.CompletionTokens, TotalTokens: out.Usage.TotalTokens},
		FinishReason: ParseFinishReason(out.Choices[0].FinishReason),
	}, nil
}
