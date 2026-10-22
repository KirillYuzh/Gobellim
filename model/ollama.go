package model

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

// OllamaConfig configures the Ollama client; an empty BaseURL defaults to
// http://localhost:11434.
type OllamaConfig struct {
	BaseURL    string
	Model      string
	HTTPClient *http.Client

	ContextTokens int
	Region        Region
}

// OllamaClient is a Client speaking Ollama's /api/chat protocol.
type OllamaClient struct {
	cfg OllamaConfig
	hc  *http.Client
}

// NewOllama builds an OllamaClient; Model is required and ContextTokens must
// not be negative, otherwise the error wraps core.ErrInvalidConfig.
func NewOllama(cfg OllamaConfig) (*OllamaClient, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://localhost:11434"
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("model: ollama: Model is required: %w", core.ErrInvalidConfig)
	}
	if cfg.ContextTokens < 0 {
		return nil, fmt.Errorf("model: ollama: ContextTokens must not be negative, got %d: %w", cfg.ContextTokens, core.ErrInvalidConfig)
	}
	return &OllamaClient{cfg: cfg, hc: httpClient(cfg.HTTPClient)}, nil
}

// Ollama is a shortcut for NewOllama with only BaseURL and Model set.
func Ollama(baseURL, modelName string) (*OllamaClient, error) {
	return NewOllama(OllamaConfig{BaseURL: baseURL, Model: modelName})
}

// Model returns the configured Ollama model name.
func (c *OllamaClient) Model() string { return c.cfg.Model }

// Region implements model.Regional.
func (c *OllamaClient) Region() Region { return c.cfg.Region }

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
	Model      string `json:"model"`
	DoneReason string `json:"done_reason"`
	Message    struct {
		Role      string           `json:"role"`
		Content   string           `json:"content"`
		ToolCalls []ollamaToolCall `json:"tool_calls"`
	} `json:"message"`
	PromptEvalCount int  `json:"prompt_eval_count"`
	EvalCount       int  `json:"eval_count"`
	Done            bool `json:"done"`
}

// Chat performs one non-streaming Ollama request, sending tools and, in
// SchemaNative mode, the JSON Schema as the request format.
// parseObject decodes a JSON string into a map and returns an error on bad
// input, so a malformed tool call cannot silently become an empty object.
func parseObject(s string) (map[string]any, error) {
	if s == "" {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// Chat performs one non-streaming Ollama request.
func (c *OllamaClient) Chat(ctx context.Context, req Request) (Response, error) {
	msgs, native := SchemaPlan(req)

	body := ollamaRequest{Model: c.cfg.Model, Stream: false}
	for _, m := range msgs {
		om := ollamaMessage{Role: string(m.Role), Content: m.Content}
		for _, tc := range m.ToolCalls {
			args, err := parseObject(tc.Args)
			if err != nil {
				return Response{}, fmt.Errorf("model: ollama: tool %s: %w", tc.Name, err)
			}
			otc := ollamaToolCall{}
			otc.Function.Name = tc.Name
			otc.Function.Arguments = args
			om.ToolCalls = append(om.ToolCalls, otc)
		}
		body.Messages = append(body.Messages, om)
	}
	if req.ToolChoice != "none" {
		for _, t := range req.Tools {
			var spec oaiToolSpec
			spec.Type = "function"
			spec.Function.Name = t.Name
			spec.Function.Description = t.Description
			spec.Function.Parameters = t.Schema
			body.Tools = append(body.Tools, spec)
		}
	}
	if native {
		body.Format = req.JSONSchema
	}
	opts := map[string]any{}
	if req.Temperature != nil {
		opts["temperature"] = *req.Temperature
	}
	if req.MaxTokens > 0 {
		opts["num_predict"] = req.MaxTokens
	}
	if req.Seed != nil {
		opts["seed"] = *req.Seed
	}
	if len(req.Stop) > 0 {
		opts["stop"] = req.Stop
	}
	if len(opts) > 0 {
		body.Options = opts
	}

	outBody, err := ApplyProviderOptions(req, body)
	if err != nil {
		return Response{}, err
	}

	var out ollamaResponse
	if err := DoJSON(ctx, c.hc, http.MethodPost, joinURL(c.cfg.BaseURL, "/api/chat"), nil, outBody, &out); err != nil {
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
		Message:      msg,
		Model:        out.Model,
		Usage:        Usage{PromptTokens: out.PromptEvalCount, CompletionTokens: out.EvalCount, TotalTokens: out.PromptEvalCount + out.EvalCount},
		FinishReason: ollamaFinishReason(out),
	}, nil
}

func ollamaFinishReason(out ollamaResponse) FinishReason {
	if out.DoneReason != "" {
		return ParseFinishReason(out.DoneReason)
	}
	if len(out.Message.ToolCalls) > 0 {
		return FinishToolCalls
	}
	if out.Done {
		return FinishStop
	}
	return FinishOther
}
