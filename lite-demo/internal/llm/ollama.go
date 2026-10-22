// Package llm адаптирует /api/chat Ollama к типам демо. Пакет внутренний:
// пример держит собственный адаптер провайдера, а не берёт библиотечный.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"some-project/internal/entity"
)

const maxResponseBytes = 4 << 20

// Ollama — клиент /api/chat.
type Ollama struct {
	base  string
	model string
	hc    *http.Client
}

// NewOllama создаёт клиент.
func NewOllama(base, model string) *Ollama {
	return &Ollama{base: base, model: model, hc: &http.Client{Timeout: 90 * time.Second}}
}

type apiRequest struct {
	Model    string            `json:"model"`
	Messages []apiMessage      `json:"messages"`
	Tools    []entity.ToolSpec `json:"tools,omitempty"`
	Stream   bool              `json:"stream"`
}

type apiMessage struct {
	Role      string        `json:"role"`
	Content   string        `json:"content"`
	Name      string        `json:"name,omitempty"`
	ToolCalls []apiToolCall `json:"tool_calls,omitempty"`
}

type apiToolCall struct {
	Function apiFunction `json:"function"`
}

type apiFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type apiResponse struct {
	Message struct {
		Role      string        `json:"role"`
		Content   string        `json:"content"`
		ToolCalls []apiToolCall `json:"tool_calls"`
	} `json:"message"`
	Error string `json:"error,omitempty"`
}

// Chat отправляет историю и доступные инструменты в Ollama.
func (c *Ollama) Chat(ctx context.Context, req entity.CompletionRequest) (entity.Completion, error) {
	body := apiRequest{Model: c.model, Stream: false, Tools: req.Tools}
	for _, m := range req.Messages {
		body.Messages = append(body.Messages, toAPIMessage(m))
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return entity.Completion{}, fmt.Errorf("ollama: encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/chat", bytes.NewReader(raw))
	if err != nil {
		return entity.Completion{}, fmt.Errorf("ollama: request: %w: %w", err, entity.ErrUpstream)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return entity.Completion{}, fmt.Errorf("ollama: %w: %w", err, entity.ErrUpstream)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return entity.Completion{}, fmt.Errorf("ollama: read: %w: %w", err, entity.ErrUpstream)
	}
	if resp.StatusCode != http.StatusOK {
		return entity.Completion{}, fmt.Errorf("ollama: status %d: %w", resp.StatusCode, entity.ErrUpstream)
	}
	var out apiResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return entity.Completion{}, fmt.Errorf("ollama: decode: %w: %w", err, entity.ErrUpstream)
	}
	if out.Error != "" {
		return entity.Completion{}, fmt.Errorf("ollama: %s: %w", out.Error, entity.ErrUpstream)
	}

	res := entity.Completion{Content: out.Message.Content}
	for _, tc := range out.Message.ToolCalls {
		args := map[string]any{}
		if len(tc.Function.Arguments) > 0 {
			if err := json.Unmarshal(tc.Function.Arguments, &args); err != nil {
				return entity.Completion{}, fmt.Errorf("ollama: tool args: %w: %w", err, entity.ErrUpstream)
			}
		}
		res.ToolCalls = append(res.ToolCalls, entity.ToolCall{Name: tc.Function.Name, Args: args})
	}
	return res, nil
}

func toAPIMessage(m entity.Message) apiMessage {
	out := apiMessage{Role: m.Role, Content: m.Content, Name: m.Name}
	for _, tc := range m.ToolCalls {
		args := tc.Args
		if args == nil {
			args = map[string]any{}
		}
		raw, _ := json.Marshal(args)
		out.ToolCalls = append(out.ToolCalls, apiToolCall{
			Function: apiFunction{Name: tc.Name, Arguments: raw},
		})
	}
	return out
}
