// Package gigachat is a gobellim.Model adapter for the GigaChat API.
//
// It uses the freemium OAuth flow: the caller passes the Authorization key from
// GigaChat Studio and the adapter exchanges it for a short-lived access token.
//
// GigaChat uses the legacy `functions`/`function_call` shape rather than
// OpenAI's `tools`. The adapter maps gobellim.ToolSpec and ToolCall to that
// shape. It also appends the requested JSON schema to the system message,
// because GigaChat does not enforce response schemas natively.
//
// The legacy shape carries at most one function call per message, and the
// adapter honors that both ways: a response becomes one ToolCall and a request
// carries one function_call. Parallel tool calls therefore never reach the
// core loop, so a Task run under this adapter makes one call at a time.
package gigachat

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	gobellim "github.com/KirillYuzh/Gobellim"
)

// Config configures the adapter.
type Config struct {
	// AuthKey is the Authorization key from GigaChat Studio. It is either the
	// base64 key itself or "client_id:client_secret".
	AuthKey    string
	Scope      string
	BaseURL    string
	OAuthURL   string
	Model      string
	HTTPClient *http.Client
	// InsecureTLS skips certificate verification. It exists for environments
	// without the Russian trusted root CA and must be opt-in.
	InsecureTLS bool
}

const (
	defaultScope   = "GIGACHAT_API_PERS"
	defaultBaseURL = "https://gigachat.devices.sberbank.ru/api/v1"
	defaultOAuth   = "https://ngw.devices.sberbank.ru:9443/api/v2/oauth"
	defaultModel   = "GigaChat"
)

// Model implements gobellim.Model.
type Model struct {
	cfg    Config
	client *http.Client

	mu      sync.Mutex
	token   string
	expires time.Time
	refresh sync.Mutex
}

var _ gobellim.Model = (*Model)(nil)

// New builds a GigaChat model.
func New(cfg Config) (*Model, error) {
	if cfg.AuthKey == "" {
		return nil, fmt.Errorf("gigachat: auth key is required")
	}
	if cfg.Scope == "" {
		cfg.Scope = defaultScope
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.OAuthURL == "" {
		cfg.OAuthURL = defaultOAuth
	}
	if cfg.Model == "" {
		cfg.Model = defaultModel
	}
	if cfg.HTTPClient == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		if cfg.InsecureTLS {
			// #nosec G402 -- explicit opt-in for environments without the CA.
			transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		}
		cfg.HTTPClient = &http.Client{Timeout: 60 * time.Second, Transport: transport}
	}
	return &Model{cfg: cfg, client: cfg.HTTPClient}, nil
}

// Chat sends one request to GigaChat. It retries 429 and 5xx with a bounded
// backoff, and re-authenticates once on 401.
func (m *Model) Chat(ctx context.Context, req gobellim.Request) (gobellim.Response, error) {
	body, err := m.encode(req)
	if err != nil {
		return gobellim.Response{}, err
	}
	token, err := m.accessToken(ctx)
	if err != nil {
		return gobellim.Response{}, err
	}
	resp, err := m.chatWithRetry(ctx, token, body)
	if err != nil {
		return gobellim.Response{}, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		_ = resp.Body.Close()
		m.invalidate()
		token, err = m.accessToken(ctx)
		if err != nil {
			return gobellim.Response{}, err
		}
		resp, err = m.chatWithRetry(ctx, token, body)
		if err != nil {
			return gobellim.Response{}, err
		}
	}
	return m.decode(resp)
}

// chatWithRetry retries the rate limit and transient server errors the same
// way the provider documents: a few attempts with an exponential delay. The
// last response is returned even if it is still an error, so decode can report
// it.
func (m *Model) chatWithRetry(ctx context.Context, token string, body []byte) (*http.Response, error) {
	const maxAttempts = 3
	delay := 500 * time.Millisecond
	for attempt := 0; ; attempt++ {
		resp, err := m.doChat(ctx, token, body)
		if err != nil {
			return nil, err
		}
		if attempt == maxAttempts-1 || (resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500) {
			return resp, nil
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		_ = resp.Body.Close()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		delay *= 2
	}
}

func (m *Model) doChat(ctx context.Context, token string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gigachat: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gigachat: chat: %w", err)
	}
	return resp, nil
}

func (m *Model) decode(resp *http.Response) (gobellim.Response, error) {
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return gobellim.Response{}, fmt.Errorf("gigachat: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The body may echo the prompt; the status is enough to classify the
		// failure, and the core runtime already keeps provider text out of audit.
		return gobellim.Response{}, fmt.Errorf("gigachat: chat returned %s", resp.Status)
	}
	var cr chatResponse
	if err := json.Unmarshal(data, &cr); err != nil {
		return gobellim.Response{}, fmt.Errorf("gigachat: decode response: %w", err)
	}
	if len(cr.Choices) == 0 {
		return gobellim.Response{}, fmt.Errorf("gigachat: response has no choices")
	}
	choice := cr.Choices[0]
	msg := gobellim.Message{Role: gobellim.RoleAssistant, Content: stripFences(choice.Message.Content)}
	finish := mapFinish(choice.FinishReason)
	if fc := choice.Message.FunctionCall; fc != nil && fc.Name != "" {
		msg.ToolCalls = []gobellim.ToolCall{{ID: newID("call_"), Name: fc.Name, Args: fc.Arguments}}
		finish = gobellim.FinishToolCalls
	}
	return gobellim.Response{
		Message: msg,
		Usage: gobellim.Usage{
			PromptTokens:     cr.Usage.PromptTokens,
			CompletionTokens: cr.Usage.CompletionTokens,
			TotalTokens:      cr.Usage.TotalTokens,
		},
		Model:        cr.Model,
		FinishReason: finish,
	}, nil
}

func (m *Model) accessToken(ctx context.Context) (string, error) {
	if token := m.cachedToken(); token != "" {
		return token, nil
	}
	// One refresh at a time: a burst of chats must not start a burst of OAuth
	// exchanges.
	m.refresh.Lock()
	defer m.refresh.Unlock()
	if token := m.cachedToken(); token != "" {
		return token, nil
	}

	form := url.Values{"scope": {m.cfg.Scope}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.cfg.OAuthURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("gigachat: build oauth request: %w", err)
	}
	req.Header.Set("Authorization", "Basic "+m.basicAuth())
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("RqUID", newUUID())

	resp, err := m.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("gigachat: oauth: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("gigachat: read oauth response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gigachat: oauth returned %s", resp.Status)
	}
	var tr tokenResponse
	if err := json.Unmarshal(data, &tr); err != nil {
		return "", fmt.Errorf("gigachat: decode oauth response: %w", err)
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("gigachat: oauth response has no access token")
	}
	expires := time.UnixMilli(tr.ExpiresAt).Add(-time.Minute)
	if tr.ExpiresAt == 0 {
		expires = time.Now().Add(25 * time.Minute)
	}
	m.mu.Lock()
	m.token = tr.AccessToken
	m.expires = expires
	m.mu.Unlock()
	return tr.AccessToken, nil
}

func (m *Model) cachedToken() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.token != "" && time.Now().Before(m.expires) {
		return m.token
	}
	return ""
}

func (m *Model) invalidate() {
	m.mu.Lock()
	m.token = ""
	m.expires = time.Time{}
	m.mu.Unlock()
}

func (m *Model) basicAuth() string {
	if strings.Contains(m.cfg.AuthKey, ":") {
		return base64.StdEncoding.EncodeToString([]byte(m.cfg.AuthKey))
	}
	return m.cfg.AuthKey
}

func (m *Model) encode(req gobellim.Request) ([]byte, error) {
	schema := ""
	if len(req.JSONSchema) > 0 {
		schema = "\n\nReturn only a JSON object matching this schema. Do not wrap it in markdown.\n" + string(req.JSONSchema)
	}
	messages := make([]chatMessage, 0, len(req.Messages))
	schemaAdded := false
	for _, msg := range req.Messages {
		cm := chatMessage{Role: string(msg.Role), Content: msg.Content, Name: msg.Name}
		switch msg.Role {
		case gobellim.RoleTool:
			cm.Role = "function"
		case gobellim.RoleAssistant:
			if len(msg.ToolCalls) > 0 {
				tc := msg.ToolCalls[0]
				cm.FunctionCall = &functionCall{Name: tc.Name, Arguments: tc.Args}
			}
		case gobellim.RoleSystem:
			if schema != "" && !schemaAdded {
				cm.Content += schema
				schemaAdded = true
			}
		}
		messages = append(messages, cm)
	}
	if schema != "" && !schemaAdded {
		messages = append([]chatMessage{{Role: "system", Content: strings.TrimSpace(schema)}}, messages...)
	}

	out := chatRequest{
		Model:     m.cfg.Model,
		Messages:  messages,
		MaxTokens: req.MaxTokens,
	}
	for _, tool := range req.Tools {
		params := tool.Schema
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object"}`)
		}
		out.Functions = append(out.Functions, function{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  params,
		})
	}
	if len(out.Functions) > 0 {
		out.FunctionCall = "auto"
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("gigachat: encode request: %w", err)
	}
	return body, nil
}

type chatRequest struct {
	Model        string        `json:"model"`
	Messages     []chatMessage `json:"messages"`
	Functions    []function    `json:"functions,omitempty"`
	FunctionCall any           `json:"function_call,omitempty"`
	MaxTokens    int           `json:"max_tokens,omitempty"`
}

type chatMessage struct {
	Role         string        `json:"role"`
	Content      string        `json:"content,omitempty"`
	Name         string        `json:"name,omitempty"`
	FunctionCall *functionCall `json:"function_call,omitempty"`
}

type functionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type function struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Role         string        `json:"role"`
			Content      string        `json:"content"`
			FunctionCall *functionCall `json:"function_call"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Model string `json:"model"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresAt   int64  `json:"expires_at"`
}

func mapFinish(reason string) gobellim.FinishReason {
	switch strings.ToLower(reason) {
	case "stop":
		return gobellim.FinishStop
	case "function_call", "tool_calls":
		return gobellim.FinishToolCalls
	case "length":
		return gobellim.FinishLength
	default:
		return gobellim.FinishOther
	}
}

// stripFences removes a markdown code fence around a JSON answer. GigaChat
// often wraps JSON in ```json even when asked not to; the core decoder stays
// strict, so normalization belongs to the adapter.
func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
	return s
}

func newID(prefix string) string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

// newUUID returns a version 4 UUID for the RqUID header. GigaChat requires the
// header shape, so rand.Text is not an alternative here.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
