package model

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gobellim/kit/internal/core"
)

type OllamaConfig struct {
	BaseURL    string
	Model      string
	HTTPClient *http.Client
}

type OllamaClient struct {
	cfg OllamaConfig
	hc  *http.Client
}

func NewOllama(cfg OllamaConfig) (*OllamaClient, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://localhost:11434"
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("model: ollama: Model is required: %w", core.ErrInvalidConfig)
	}
	return &OllamaClient{cfg: cfg, hc: httpClient(cfg.HTTPClient)}, nil
}

func Ollama(baseURL, modelName string) (*OllamaClient, error) {
	return NewOllama(OllamaConfig{BaseURL: baseURL, Model: modelName})
}

func (c *OllamaClient) Model() string { return c.cfg.Model }

type ollamaToolCall struct {
	Function struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	} `json:"function"`
}

type ollamaMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
}

type ollamaRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Tools    []oaiToolSpec   `json:"tools,omitempty"`
	Format   json.RawMessage `json:"format,omitempty"`
	Options  map[string]any  `json:"options,omitempty"`
	Stream   bool            `json:"stream"`
}

type ollamaResponse struct {
	Model   string `json:"model"`
	Message struct {
		Role      string           `json:"role"`
		Content   string           `json:"content"`
		ToolCalls []ollamaToolCall `json:"tool_calls"`
	} `json:"message"`
	PromptEvalCount int  `json:"prompt_eval_count"`
	EvalCount       int  `json:"eval_count"`
	Done            bool `json:"done"`
}

func (c *OllamaClient) Chat(ctx context.Context, req Request) (Response, error) {
	body := ollamaRequest{Model: c.cfg.Model, Stream: false}
	for _, m := range req.Messages {
		om := ollamaMessage{Role: string(m.Role), Content: m.Content}
		for _, tc := range m.ToolCalls {
			var otc ollamaToolCall
			otc.Function.Name = tc.Name
			otc.Function.Arguments = rawObject(tc.Args)
			om.ToolCalls = append(om.ToolCalls, otc)
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
	if len(req.JSONSchema) > 0 {
		body.Format = req.JSONSchema
	}
	opts := map[string]any{}
	if req.Temperature != nil {
		opts["temperature"] = *req.Temperature
	}
	if req.MaxTokens > 0 {
		opts["num_predict"] = req.MaxTokens
	}
	if len(opts) > 0 {
		body.Options = opts
	}

	var out ollamaResponse
	if err := doJSON(ctx, c.hc, http.MethodPost, joinURL(c.cfg.BaseURL, "/api/chat"), nil, body, &out); err != nil {
		return Response{}, err
	}
	msg := Message{Role: RoleAssistant, Content: out.Message.Content}
	for i, tc := range out.Message.ToolCalls {
		args, err := json.Marshal(tc.Function.Arguments)
		if err != nil {
			return Response{}, fmt.Errorf("model: ollama: encode tool args: %w", err)
		}
		msg.ToolCalls = append(msg.ToolCalls, ToolCall{
			ID:   fmt.Sprintf("%s-%d", tc.Function.Name, i),
			Name: tc.Function.Name,
			Args: string(args),
		})
	}
	return Response{
		Message: msg,
		Model:   out.Model,
		Usage:   Usage{PromptTokens: out.PromptEvalCount, CompletionTokens: out.EvalCount, TotalTokens: out.PromptEvalCount + out.EvalCount},
	}, nil
}

func rawObject(s string) map[string]any {
	if s == "" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}
