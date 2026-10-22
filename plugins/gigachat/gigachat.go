// Package gigachat implements GigaChat chat and embedding clients for
// gobellim. It is an optional plugin: the core model and embed packages expose
// the interfaces, this package supplies the GigaChat adapters.
package gigachat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/KirillYuzh/Gobellim/internal/core"
	"github.com/KirillYuzh/Gobellim/internal/httpjson"
	"github.com/KirillYuzh/Gobellim/internal/oauth"
	"github.com/KirillYuzh/Gobellim/model"
)

const (
	// DefaultBaseURL is the default GigaChat API endpoint.
	DefaultBaseURL = "https://api.giga.chat"
	// DefaultAuthURL is the default OAuth token endpoint.
	DefaultAuthURL = "https://ngw.devices.sberbank.ru:9443/api/v2/oauth"
	// DefaultScope is the default OAuth scope.
	DefaultScope = "GIGACHAT_API_PERS"
	// DefaultModel is the chat model used when Config.Model is empty.
	DefaultModel = "GigaChat-2"
	// DefaultEmbedModel is the embedding model used when EmbedConfig.Model is
	// empty.
	DefaultEmbedModel = "Embeddings"
)

// Config configures the GigaChat chat client; an empty BaseURL, AuthURL, Scope
// or Model falls back to the matching Default* constant. Credentials is a
// required base64 "client_id:client_secret" pair.
type Config struct {
	BaseURL       string
	AuthURL       string
	Credentials   string
	Scope         string
	Model         string
	HTTPClient    *http.Client
	ContextTokens int
}

// Client is a model.Client backed by GigaChat's chat API, authenticating with
// OAuth bearer tokens.
type Client struct {
	cfg Config
	hc  *http.Client
	ts  *oauth.TokenSource
}

// New builds a GigaChat chat client, applying default URLs, scope and model; it
// returns an error wrapping core.ErrInvalidConfig when Credentials is empty or
// ContextTokens is negative.
func New(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.AuthURL == "" {
		cfg.AuthURL = DefaultAuthURL
	}
	if cfg.Scope == "" {
		cfg.Scope = DefaultScope
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	if cfg.Credentials == "" {
		return nil, fmt.Errorf("gigachat: Credentials is required: %w", core.ErrInvalidConfig)
	}
	if cfg.ContextTokens < 0 {
		return nil, fmt.Errorf("gigachat: ContextTokens must not be negative, got %d: %w", cfg.ContextTokens, core.ErrInvalidConfig)
	}
	hc := httpjson.Client(cfg.HTTPClient)
	return &Client{
		cfg: cfg,
		hc:  hc,
		ts:  &oauth.TokenSource{AuthURL: cfg.AuthURL, Credentials: cfg.Credentials, Scope: cfg.Scope, HTTPClient: hc},
	}, nil
}

// Model returns the configured GigaChat model name.
func (c *Client) Model() string { return c.cfg.Model }

// ContextTokens returns the configured context window in tokens.
func (c *Client) ContextTokens() int { return c.cfg.ContextTokens }

// SupportsNativeSchema reports false: GigaChat has no native schema field, so
// callers inline the schema.
func (c *Client) SupportsNativeSchema() bool { return false }

// Chat performs one non-streaming GigaChat request: tools are sent as
// functions, and an empty choices list yields an error wrapping
// core.ErrProvider.
func (c *Client) Chat(ctx context.Context, req model.Request) (model.Response, error) {
	token, err := c.ts.Token(ctx)
	if err != nil {
		return model.Response{}, err
	}

	msgs, native := model.SchemaPlan(req)

	body := gcRequest{
		Model:       c.cfg.Model,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}
	for _, m := range msgs {
		role := string(m.Role)
		if m.Role == model.RoleTool {
			role = "function"
		}
		content := m.Content
		if m.Role == model.RoleTool && !json.Valid([]byte(content)) {
			// GigaChat parses a function result as JSON. A spotlighted tool result is
			// plain text wrapped in <untrusted-data>, so keep the wrapper and put it
			// in a JSON envelope: the provider accepts it and the model still sees
			// the spotlit text.
			wrapped, err := json.Marshal(map[string]string{"result": content})
			if err != nil {
				return model.Response{}, fmt.Errorf("gigachat: encode function result: %w: %w", err, core.ErrProvider)
			}
			content = string(wrapped)
		}
		gm := gcMessage{Role: role, Content: content, Name: m.Name}
		if m.Role == model.RoleAssistant && len(m.ToolCalls) > 0 {
			tc := m.ToolCalls[0]
			gm.FunctionCall = &gcFunctionCall{Name: tc.Name, Arguments: model.RawObject(tc.Args)}
		}
		body.Messages = append(body.Messages, gm)
	}
	if req.ToolChoice != "none" {
		for _, t := range req.Tools {
			body.Functions = append(body.Functions, gcFunction{Name: t.Name, Description: t.Description, Parameters: t.Schema})
		}
		switch {
		case native:
			body.Functions = append(body.Functions, gcFunction{Name: structuredFunctionName, Description: "Return the structured result.", Parameters: req.JSONSchema})
			body.FunctionCall = map[string]string{"name": structuredFunctionName}
		case len(body.Functions) > 0:
			body.FunctionCall = "auto"
		}
	}

	headers := map[string]string{
		"Authorization": "Bearer " + token,
		"Accept":        "application/json",
	}
	outBody, err := model.ApplyProviderOptions(req, body)
	if err != nil {
		return model.Response{}, err
	}

	var out gcResponse
	if err := model.DoJSON(ctx, c.hc, http.MethodPost, httpjson.JoinURL(c.cfg.BaseURL, "/v1/chat/completions"), headers, outBody, &out); err != nil {
		return model.Response{}, err
	}
	if len(out.Choices) == 0 {
		return model.Response{}, fmt.Errorf("gigachat: empty choices: %w", core.ErrProvider)
	}
	choice := out.Choices[0].Message
	msg := model.Message{Role: model.RoleAssistant, Content: choice.Content}
	if choice.FunctionCall != nil {
		args, err := json.Marshal(choice.FunctionCall.Arguments)
		if err != nil {
			return model.Response{}, fmt.Errorf("gigachat: encode tool args: %w", err)
		}
		if choice.FunctionCall.Name == structuredFunctionName {
			msg.Content = string(args)
		} else {
			msg.ToolCalls = append(msg.ToolCalls, model.ToolCall{
				ID:   fmt.Sprintf("gc-%s", choice.FunctionCall.Name),
				Name: choice.FunctionCall.Name,
				Args: string(args),
			})
		}
	}
	return model.Response{
		Message:      msg,
		Model:        out.Model,
		Usage:        model.Usage{PromptTokens: out.Usage.PromptTokens, CompletionTokens: out.Usage.CompletionTokens, TotalTokens: out.Usage.TotalTokens},
		FinishReason: gcFinishReason(out.Choices[0].FinishReason, choice.FunctionCall),
	}, nil
}

const structuredFunctionName = "structured_output"

type gcFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type gcFunctionCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type gcMessage struct {
	Role         string          `json:"role"`
	Content      string          `json:"content"`
	Name         string          `json:"name,omitempty"`
	FunctionCall *gcFunctionCall `json:"function_call,omitempty"`
}

type gcRequest struct {
	Model        string       `json:"model"`
	Messages     []gcMessage  `json:"messages"`
	Functions    []gcFunction `json:"functions,omitempty"`
	FunctionCall any          `json:"function_call,omitempty"`
	Temperature  *float64     `json:"temperature,omitempty"`
	MaxTokens    int          `json:"max_tokens,omitempty"`
}

type gcResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Role         string          `json:"role"`
			Content      string          `json:"content"`
			FunctionCall *gcFunctionCall `json:"function_call"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func gcFinishReason(reason string, call *gcFunctionCall) model.FinishReason {
	if reason != "" {
		return model.ParseFinishReason(reason)
	}
	if call != nil {
		return model.FinishToolCalls
	}
	return model.FinishStop
}
