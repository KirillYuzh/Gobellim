package model

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gobellim/kit/internal/core"
	"github.com/gobellim/kit/internal/oauth"
)

const (
	GigaChatDefaultBaseURL = "https://api.giga.chat"
	GigaChatDefaultAuthURL = "https://ngw.devices.sberbank.ru:9443/api/v2/oauth"
	GigaChatDefaultScope   = "GIGACHAT_API_PERS"
	GigaChatDefaultModel   = "GigaChat-2"
)

type GigaChatConfig struct {
	BaseURL     string
	AuthURL     string
	Credentials string
	Scope       string
	Model       string
	HTTPClient  *http.Client
}

type GigaChatClient struct {
	cfg GigaChatConfig
	hc  *http.Client
	ts  *oauth.TokenSource
}

func GigaChat(cfg GigaChatConfig) (*GigaChatClient, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = GigaChatDefaultBaseURL
	}
	if cfg.AuthURL == "" {
		cfg.AuthURL = GigaChatDefaultAuthURL
	}
	if cfg.Scope == "" {
		cfg.Scope = GigaChatDefaultScope
	}
	if cfg.Model == "" {
		cfg.Model = GigaChatDefaultModel
	}
	if cfg.Credentials == "" {
		return nil, fmt.Errorf("model: gigachat: Credentials is required: %w", core.ErrInvalidConfig)
	}
	hc := httpClient(cfg.HTTPClient)
	return &GigaChatClient{
		cfg: cfg,
		hc:  hc,
		ts:  &oauth.TokenSource{AuthURL: cfg.AuthURL, Credentials: cfg.Credentials, Scope: cfg.Scope, HTTPClient: hc},
	}, nil
}

func (c *GigaChatClient) Model() string { return c.cfg.Model }

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
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

const structuredFunctionName = "structured_output"

func (c *GigaChatClient) Chat(ctx context.Context, req Request) (Response, error) {
	token, err := c.ts.Token(ctx)
	if err != nil {
		return Response{}, err
	}

	body := gcRequest{
		Model:       c.cfg.Model,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}
	for _, m := range req.Messages {
		role := string(m.Role)
		if m.Role == RoleTool {
			role = "function"
		}
		gm := gcMessage{Role: role, Content: m.Content, Name: m.Name}
		if m.Role == RoleAssistant && len(m.ToolCalls) > 0 {
			tc := m.ToolCalls[0]
			gm.FunctionCall = &gcFunctionCall{Name: tc.Name, Arguments: rawObject(tc.Args)}
		}
		body.Messages = append(body.Messages, gm)
	}
	for _, t := range req.Tools {
		body.Functions = append(body.Functions, gcFunction{Name: t.Name, Description: t.Description, Parameters: t.Schema})
	}
	switch {
	case len(req.JSONSchema) > 0:
		body.Functions = append(body.Functions, gcFunction{Name: structuredFunctionName, Description: "Return the structured result.", Parameters: req.JSONSchema})
		body.FunctionCall = map[string]string{"name": structuredFunctionName}
	case len(body.Functions) > 0:
		body.FunctionCall = "auto"
	}

	headers := map[string]string{
		"Authorization": "Bearer " + token,
		"Accept":        "application/json",
	}
	var out gcResponse
	if err := doJSON(ctx, c.hc, http.MethodPost, joinURL(c.cfg.BaseURL, "/v1/chat/completions"), headers, body, &out); err != nil {
		return Response{}, err
	}
	if len(out.Choices) == 0 {
		return Response{}, fmt.Errorf("model: gigachat: empty choices: %w", core.ErrProvider)
	}
	choice := out.Choices[0].Message
	msg := Message{Role: RoleAssistant, Content: choice.Content}
	if choice.FunctionCall != nil {
		args, err := json.Marshal(choice.FunctionCall.Arguments)
		if err != nil {
			return Response{}, fmt.Errorf("model: gigachat: encode tool args: %w", err)
		}
		if choice.FunctionCall.Name == structuredFunctionName {
			msg.Content = string(args)
		} else {
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{
				ID:   fmt.Sprintf("gc-%s", choice.FunctionCall.Name),
				Name: choice.FunctionCall.Name,
				Args: string(args),
			})
		}
	}
	return Response{
		Message: msg,
		Model:   out.Model,
		Usage:   Usage{PromptTokens: out.Usage.PromptTokens, CompletionTokens: out.Usage.CompletionTokens, TotalTokens: out.Usage.TotalTokens},
	}, nil
}
