package model

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gobellim/kit/internal/core"
	"github.com/gobellim/kit/internal/httpjson"
)

type oaiStreamChunk struct {
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// ChatStream streams an OpenAI-compatible chat completion over SSE. Content
// deltas go to onDelta in order; tool-call fragments are reassembled and
// returned in the final Response, so streaming is a drop-in for Chat.
func (c *OpenAIClient) ChatStream(ctx context.Context, req Request, onDelta func(string) error) (Response, error) {
	payload, headers, err := c.encode(req, true)
	if err != nil {
		return Response{}, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Response{}, fmt.Errorf("model: openai stream: encode: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, joinURL(c.cfg.BaseURL, "/chat/completions"), bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("model: openai stream: request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return Response{}, errors.Join(fmt.Errorf("model: openai stream: %w", err), core.ErrProvider)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Response{}, fmt.Errorf("model: openai stream: status %d: %s: %w",
			resp.StatusCode, httpjson.Snippet(snippet), core.ErrProvider)
	}
	return readOpenAIStream(resp.Body, onDelta)
}

func readOpenAIStream(r io.Reader, onDelta func(string) error) (Response, error) {
	var (
		content   strings.Builder
		modelName string
		finish    string
		usage     Usage
		calls     = map[int]*ToolCall{}
		order     []int
	)

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}
		var chunk oaiStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return Response{}, fmt.Errorf("model: openai stream: decode chunk: %w: %w", err, core.ErrProvider)
		}
		if chunk.Model != "" {
			modelName = chunk.Model
		}
		if chunk.Usage.PromptTokens > 0 || chunk.Usage.TotalTokens > 0 {
			usage = Usage{
				PromptTokens:     chunk.Usage.PromptTokens,
				CompletionTokens: chunk.Usage.CompletionTokens,
				TotalTokens:      chunk.Usage.TotalTokens,
			}
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Content != "" {
				content.WriteString(ch.Delta.Content)
				if err := onDelta(ch.Delta.Content); err != nil {
					return Response{}, err
				}
			}
			for _, tc := range ch.Delta.ToolCalls {
				call, ok := calls[tc.Index]
				if !ok {
					call = &ToolCall{}
					calls[tc.Index] = call
					order = append(order, tc.Index)
				}
				if tc.ID != "" {
					call.ID = tc.ID
				}
				if tc.Function.Name != "" {
					call.Name = tc.Function.Name
				}
				call.Args += tc.Function.Arguments
			}
			if ch.FinishReason != "" {
				finish = ch.FinishReason
			}
		}
	}
	if err := sc.Err(); err != nil {
		return Response{}, fmt.Errorf("model: openai stream: read: %w: %w", err, core.ErrProvider)
	}

	msg := Message{Role: RoleAssistant, Content: content.String()}
	for _, i := range order {
		msg.ToolCalls = append(msg.ToolCalls, *calls[i])
	}
	return Response{
		Message:      msg,
		Model:        modelName,
		Usage:        usage,
		FinishReason: ParseFinishReason(finish),
	}, nil
}
