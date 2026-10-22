// Package entity — общий словарь сервиса: запросы, ответы, ошибки и бюджет.
package entity

import (
	"context"
	"encoding/json"
	"fmt"
)

// User — principal запроса. В демо он приходит в теле; в проде здесь JWT/OIDC,
// а usecase не меняется.
type User struct {
	ID     string   `json:"id"`
	Name   string   `json:"name,omitempty"`
	Tenant string   `json:"tenant"`
	Roles  []string `json:"roles"`
}

// Validate проверяет форму principal.
func (u User) Validate() error {
	if u.ID == "" || u.Tenant == "" || len(u.Roles) == 0 {
		return fmt.Errorf("user id, tenant and roles are required: %w", ErrInvalid)
	}
	return nil
}

// Message — одно сообщение истории диалога.
type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	Name      string     `json:"name,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall — вызов инструмента моделью или клиентом.
type ToolCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

// ChatRequest — один ход диалога. RunID продолжает приостановленный сценарий.
// ChatRequest — один ход диалога.
type ChatRequest struct {
	User     User      `json:"user"`
	Messages []Message `json:"messages"`
}

// ResumeRequest — ответ пользователя на вопрос приостановленного сценария.
// Text для свободного ответа, Choice для выбора из Options. ResumeID делает
// повтор безопасным.
type ResumeRequest struct {
	User     User   `json:"user"`
	ResumeID string `json:"resume_id,omitempty"`
	Text     string `json:"text,omitempty"`
	Choice   string `json:"choice,omitempty"`
}

// ChatResponse — ответ сервиса. Status: ok, awaiting_input, max_steps или budget.
type ChatResponse struct {
	Status    string     `json:"status"`
	Answer    string     `json:"answer,omitempty"`
	RunID     string     `json:"run_id,omitempty"`
	Question  *Question  `json:"question,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// Question — типизированный вопрос приостановленного сценария. Input говорит
// клиенту, что присылать в resume: text или choice.
type Question struct {
	Prompt  string   `json:"prompt"`
	Input   string   `json:"input"`
	Options []string `json:"options,omitempty"`
}

// ResumeResponse — результат продолжения: готовый ответ или следующий вопрос.
type ResumeResponse struct {
	Status   string    `json:"status"`
	RunID    string    `json:"run_id"`
	Answer   string    `json:"answer,omitempty"`
	Question *Question `json:"question,omitempty"`
}

// Result — результат инструмента; уходит модели как JSON. Status: completed или
// awaiting_user. RunID и Append модели не показывают: они нужны приложению.
type Result struct {
	Status  string   `json:"status,omitempty"`
	Body    string   `json:"body,omitempty"`
	RunID   string   `json:"run_id,omitempty"`
	Prompt  string   `json:"prompt,omitempty"`
	Input   string   `json:"input,omitempty"`
	Options []string `json:"options,omitempty"`
	Error   string   `json:"error,omitempty"`
	Append  string   `json:"append,omitempty"`
}

// CompletionRequest — то, что уходит в LLM.
type CompletionRequest struct {
	Messages []Message
	Tools    []ToolSpec
}

// Completion — ответ LLM.
type Completion struct {
	Content   string
	ToolCalls []ToolCall
}

// ToolSpec — описание инструмента для модели.
type ToolSpec struct {
	Type     string       `json:"type"`
	Function FunctionSpec `json:"function"`
}

// FunctionSpec — имя, описание и JSON Schema аргументов.
type FunctionSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Budget ограничивает один ход чата.
type Budget struct {
	MaxSteps            int `json:"max_steps"`
	MaxToolCalls        int `json:"max_tool_calls"`
	MaxToolCallsPerStep int `json:"max_tool_calls_per_step"`
	MaxArgsBytes        int `json:"max_args_bytes"`
	MaxMessages         int `json:"max_messages"`
	MaxHistoryBytes     int `json:"max_history_bytes"`
}

// DefaultBudget возвращает дефолты демо.
func DefaultBudget() Budget {
	return Budget{
		MaxSteps:            6,
		MaxToolCalls:        16,
		MaxToolCallsPerStep: 4,
		MaxArgsBytes:        16 << 10,
		MaxMessages:         64,
		MaxHistoryBytes:     128 << 10,
	}
}

// WithDefaults заменяет неположительные поля дефолтами.
func (b Budget) WithDefaults() Budget {
	d := DefaultBudget()
	pick := func(v, def int) int {
		if v <= 0 {
			return def
		}
		return v
	}
	b.MaxSteps = pick(b.MaxSteps, d.MaxSteps)
	b.MaxToolCalls = pick(b.MaxToolCalls, d.MaxToolCalls)
	b.MaxToolCallsPerStep = pick(b.MaxToolCallsPerStep, d.MaxToolCallsPerStep)
	b.MaxArgsBytes = pick(b.MaxArgsBytes, d.MaxArgsBytes)
	b.MaxMessages = pick(b.MaxMessages, d.MaxMessages)
	b.MaxHistoryBytes = pick(b.MaxHistoryBytes, d.MaxHistoryBytes)
	return b
}

// Health — состояние сервиса и его бюджета. Это не только liveness.
type Health struct {
	Status string   `json:"status"`
	Tools  []string `json:"tools"`
	Budget Budget   `json:"budget"`
}

type userKey struct{}

// WithUser кладёт principal в контекст: инструменты и сценарии видят, от чьего
// имени идёт вызов.
func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, userKey{}, u)
}

// UserFrom достаёт principal из контекста.
func UserFrom(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(userKey{}).(User)
	return u, ok
}
