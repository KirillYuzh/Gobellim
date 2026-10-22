// Package llm — клиент GigaChat для демо. Он сам получает OAuth-токен и
// переводит сообщения демо в формат GigaChat.
package llm

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"some-project/internal/entity"
)

// Публичные корневые сертификаты Минцифры: ими подписан OAuth GigaChat.
//
//go:embed russian_root_ca.pem
var russianRootCA []byte

//go:embed russian_sub_ca.pem
var russianSubCA []byte

const (
	defaultBaseURL = "https://api.giga.chat"
	defaultAuthURL = "https://ngw.devices.sberbank.ru:9443/api/v2/oauth"
	defaultScope   = "GIGACHAT_API_PERS"
	defaultModel   = "GigaChat-2"
	maxResponse    = 4 << 20
)

// GigaChat — клиент чата. Токен кэшируется и обновляется заранее.
type GigaChat struct {
	base   string
	auth   string
	scope  string
	model  string
	key    string
	hc     *http.Client
	mu     sync.Mutex
	token  string
	expiry time.Time
}

// NewGigaChat собирает клиент. Ключ это base64-пара client_id:client_secret.
func NewGigaChat(key, model, base, auth, scope string) *GigaChat {
	if model == "" {
		model = defaultModel
	}
	if base == "" {
		base = defaultBaseURL
	}
	if auth == "" {
		auth = defaultAuthURL
	}
	if scope == "" {
		scope = defaultScope
	}
	return &GigaChat{
		base:  strings.TrimRight(base, "/"),
		auth:  auth,
		scope: scope,
		model: model,
		key:   key,
		hc:    tlsClient(),
	}
}

// tlsClient добавляет сертификаты Минцифры к системным корням.
func tlsClient() *http.Client {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	pool.AppendCertsFromPEM(russianRootCA)
	pool.AppendCertsFromPEM(russianSubCA)
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	return &http.Client{Timeout: 90 * time.Second, Transport: tr}
}

// Chat выполняет один запрос к /v1/chat/completions.
func (c *GigaChat) Chat(ctx context.Context, req entity.CompletionRequest) (entity.Completion, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return entity.Completion{}, err
	}

	body := gcRequest{Model: c.model}
	for _, m := range req.Messages {
		gm, err := toGCMessage(m)
		if err != nil {
			return entity.Completion{}, err
		}
		body.Messages = append(body.Messages, gm)
	}
	if len(req.Tools) > 0 {
		for _, t := range req.Tools {
			body.Functions = append(body.Functions, gcFunction{
				Name: t.Function.Name, Description: t.Function.Description, Parameters: t.Function.Parameters,
			})
		}
		body.FunctionCall = "auto"
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return entity.Completion{}, fmt.Errorf("gigachat: encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return entity.Completion{}, fmt.Errorf("gigachat: request: %w: %w", err, entity.ErrUpstream)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return entity.Completion{}, fmt.Errorf("gigachat: %w: %w", err, entity.ErrUpstream)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return entity.Completion{}, fmt.Errorf("gigachat: read: %w: %w", err, entity.ErrUpstream)
	}
	if resp.StatusCode != http.StatusOK {
		return entity.Completion{}, fmt.Errorf("gigachat: status %d: %w", resp.StatusCode, entity.ErrUpstream)
	}

	var out gcResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return entity.Completion{}, fmt.Errorf("gigachat: decode: %w: %w", err, entity.ErrUpstream)
	}
	if len(out.Choices) == 0 {
		return entity.Completion{}, fmt.Errorf("gigachat: empty choices: %w", entity.ErrUpstream)
	}

	msg := out.Choices[0].Message
	res := entity.Completion{Content: msg.Content}
	if msg.FunctionCall != nil {
		args := msg.FunctionCall.Arguments
		if args == nil {
			args = map[string]any{}
		}
		res.ToolCalls = []entity.ToolCall{{Name: msg.FunctionCall.Name, Args: args}}
	}
	return res, nil
}

// accessToken возвращает кэшированный токен или получает новый.
func (c *GigaChat) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.expiry) {
		return c.token, nil
	}

	form := url.Values{"scope": {c.scope}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.auth, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("gigachat: auth request: %w: %w", err, entity.ErrUpstream)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Basic "+c.key)
	req.Header.Set("RqUID", newUUID())

	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("gigachat: auth: %w: %w", err, entity.ErrUpstream)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", fmt.Errorf("gigachat: auth read: %w: %w", err, entity.ErrUpstream)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gigachat: auth status %d: %w", resp.StatusCode, entity.ErrUpstream)
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresAt   int64  `json:"expires_at"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("gigachat: auth decode: %w: %w", err, entity.ErrUpstream)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("gigachat: empty access token: %w", entity.ErrUpstream)
	}
	c.token = out.AccessToken
	c.expiry = time.Now().Add(25 * time.Minute)
	if out.ExpiresAt > 0 {
		c.expiry = time.UnixMilli(out.ExpiresAt).Add(-30 * time.Second)
	}
	return c.token, nil
}

func toGCMessage(m entity.Message) (gcMessage, error) {
	role := m.Role
	if role == "tool" {
		role = "function"
	}
	gm := gcMessage{Role: role, Content: m.Content, Name: m.Name}
	if role == "assistant" && len(m.ToolCalls) > 0 {
		if len(m.ToolCalls) > 1 {
			return gcMessage{}, fmt.Errorf("gigachat: %d tool calls in one message, provider supports one: %w", len(m.ToolCalls), entity.ErrUpstream)
		}
		args := m.ToolCalls[0].Args
		if args == nil {
			args = map[string]any{}
		}
		gm.FunctionCall = &gcFunctionCall{Name: m.ToolCalls[0].Name, Arguments: args}
	}
	return gm, nil
}

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
}

type gcResponse struct {
	Choices []struct {
		Message struct {
			Content      string          `json:"content"`
			FunctionCall *gcFunctionCall `json:"function_call"`
		} `json:"message"`
	} `json:"choices"`
}

// newUUID возвращает UUIDv4 для заголовка RqUID.
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000-0000-4000-8000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
