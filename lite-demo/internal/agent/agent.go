// Package agent — системная часть демо: цикл «модель → инструменты → модель»,
// проверка прав, бюджет вызовов и единая точка запуска инструмента.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"some-project/internal/entity"
	"some-project/internal/policy"
	"some-project/internal/tools"
)

// LLM — клиент чата. Его реализует Ollama.
type LLM interface {
	Chat(ctx context.Context, req entity.CompletionRequest) (entity.Completion, error)
}

// Config — зависимости агента.
type Config struct {
	LLM    LLM
	Policy *policy.Policy
	Tools  *tools.Set
	Budget entity.Budget
	Logger *slog.Logger
}

// Agent выполняет ходы модели и прямые вызовы инструментов.
type Agent struct {
	llm    LLM
	policy *policy.Policy
	tools  *tools.Set
	budget entity.Budget
	log    *slog.Logger
}

// New проверяет зависимости и собирает агент.
func New(c Config) (*Agent, error) {
	if c.LLM == nil || c.Policy == nil || c.Tools == nil {
		return nil, fmt.Errorf("agent: LLM, policy и tools обязательны: %w", entity.ErrConfig)
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	return &Agent{
		llm:    c.LLM,
		policy: c.Policy,
		tools:  c.Tools,
		budget: c.Budget.WithDefaults(),
		log:    c.Logger,
	}, nil
}

// Tools возвращает каталог инструментов.
func (a *Agent) Tools() *tools.Set { return a.tools }

// Budget возвращает бюджет одного запроса.
func (a *Agent) Budget() entity.Budget { return a.budget }

// Chat прогоняет историю через модель и инструменты.
func (a *Agent) Chat(ctx context.Context, user entity.User, in []entity.Message) (entity.ChatResponse, error) {
	s, err := a.session(user)
	if err != nil {
		return entity.ChatResponse{}, err
	}
	return s.chat(ctx, in)
}

func (a *Agent) session(user entity.User) (*session, error) {
	if err := user.Validate(); err != nil {
		return nil, err
	}
	return &session{a: a, user: user}, nil
}

// session — один запрос: principal и общий бюджет вызовов.
type session struct {
	a     *Agent
	user  entity.User
	calls int
}

// chat — цикл «модель → инструменты → модель» с ограничением шагов.
func (s *session) chat(ctx context.Context, in []entity.Message) (entity.ChatResponse, error) {
	if err := s.authorize("chat"); err != nil {
		return entity.ChatResponse{}, err
	}
	msgs, err := s.prepare(in)
	if err != nil {
		return entity.ChatResponse{}, err
	}
	specs := s.specs()
	msgs = append([]entity.Message{{Role: "system", Content: systemPrompt(specs)}}, msgs...)

	var display string
	var calls []entity.ToolCall
	last := ""
	for step := 0; step < s.a.budget.MaxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return entity.ChatResponse{ToolCalls: calls}, err
		}
		resp, err := s.a.llm.Chat(ctx, entity.CompletionRequest{Messages: msgs, Tools: specs})
		if err != nil {
			return entity.ChatResponse{ToolCalls: calls}, fmt.Errorf("llm: %w: %w", err, entity.ErrUpstream)
		}
		last = resp.Content
		if len(resp.ToolCalls) == 0 {
			return entity.ChatResponse{Status: "ok", Answer: appendDisplay(resp.Content, display), ToolCalls: calls}, nil
		}
		if len(resp.ToolCalls) > s.a.budget.MaxToolCallsPerStep {
			return entity.ChatResponse{Status: "budget", Answer: resp.Content, ToolCalls: calls}, nil
		}

		// Повтор внутри шага считаем циклом модели: побочный эффект не
		// запускаем второй раз и бюджет не тратим.
		seen := make(map[string]bool, len(resp.ToolCalls))
		toolMsgs := make([]entity.Message, 0, len(resp.ToolCalls))
		for _, tc := range resp.ToolCalls {
			key := callKey(tc)
			if seen[key] {
				toolMsgs = append(toolMsgs, toolMessage(tc.Name, entity.Result{Error: "duplicate tool call"}))
				continue
			}
			seen[key] = true

			res, err := s.call(ctx, tc)
			if err != nil {
				if fatal(err) {
					return entity.ChatResponse{ToolCalls: calls}, err
				}
				s.a.log.Error("tool failed", "tool", tc.Name, "user", s.user.ID, "err", err)
				res = entity.Result{Error: publicError(err)}
			}
			if res.NeedInput {
				return entity.ChatResponse{
					Status: "need_input", Question: res.Question, Query: lastUser(msgs), ToolCalls: calls,
				}, nil
			}
			if res.Display != "" {
				display = res.Display
			}
			calls = append(calls, tc)
			toolMsgs = append(toolMsgs, toolMessage(tc.Name, res))
		}
		msgs = append(msgs, entity.Message{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls})
		msgs = append(msgs, toolMsgs...)
	}
	return entity.ChatResponse{Status: "max_steps", Answer: last, ToolCalls: calls}, nil
}

func (s *session) call(ctx context.Context, tc entity.ToolCall) (entity.Result, error) {
	def := s.a.tools.Get(tc.Name)
	if def == nil {
		return entity.Result{}, entity.ErrNotFound
	}
	if err := s.authorize(def.Permission()); err != nil {
		return entity.Result{}, err
	}
	if s.calls >= s.a.budget.MaxToolCalls {
		return entity.Result{}, fmt.Errorf("tool budget: %w", entity.ErrBudget)
	}
	s.calls++

	args := tc.Args
	if args == nil {
		args = map[string]any{}
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return entity.Result{}, fmt.Errorf("tool args: %w: %w", err, entity.ErrInvalid)
	}
	if len(raw) > s.a.budget.MaxArgsBytes {
		return entity.Result{}, fmt.Errorf("tool args too large: %w", entity.ErrBudget)
	}

	s.a.log.Info("tool", "name", def.Name(), "effect", def.Effect().String(), "user", s.user.ID)
	out, err := def.Invoke(ctx, raw)
	if err != nil {
		return entity.Result{}, err
	}
	var res entity.Result
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		return entity.Result{}, fmt.Errorf("tool result: %w: %w", err, entity.ErrInternal)
	}
	return res, nil
}

func (s *session) prepare(in []entity.Message) ([]entity.Message, error) {
	if len(in) == 0 || len(in) > s.a.budget.MaxMessages {
		return nil, fmt.Errorf("message count must be in [1, %d]: %w", s.a.budget.MaxMessages, entity.ErrInvalid)
	}
	out := make([]entity.Message, 0, len(in))
	hasUser, total := false, 0
	for _, m := range in {
		if m.Role != "user" && m.Role != "assistant" {
			continue // клиент не может подсунуть system или tool
		}
		hasUser = hasUser || m.Role == "user"
		total += len(m.Content)
		if total > s.a.budget.MaxHistoryBytes {
			return nil, fmt.Errorf("history is too large: %w", entity.ErrInvalid)
		}
		out = append(out, entity.Message{Role: m.Role, Content: m.Content})
	}
	if !hasUser {
		return nil, fmt.Errorf("at least one user message is required: %w", entity.ErrInvalid)
	}
	return out, nil
}

func (s *session) specs() []entity.ToolSpec {
	out := make([]entity.ToolSpec, 0, s.a.tools.Len())
	for _, def := range s.a.tools.All() {
		if !s.a.policy.Allows(s.user, def.Permission()) {
			continue
		}
		out = append(out, entity.ToolSpec{
			Type: "function",
			Function: entity.FunctionSpec{
				Name: def.Name(), Description: def.Description(), Parameters: def.Schema(),
			},
		})
	}
	return out
}

func (s *session) authorize(permission string) error {
	if s.a.policy.Allows(s.user, permission) {
		return nil
	}
	return fmt.Errorf("%s: %w", permission, entity.ErrUnauthorized)
}

func systemPrompt(specs []entity.ToolSpec) string {
	names := make([]string, 0, len(specs))
	for _, s := range specs {
		names = append(names, s.Function.Name)
	}
	return fmt.Sprintf(
		"Ты помощник по Go. Отвечай кратко.\n"+
			"Доступные инструменты: %s. Вызывай их, когда пользователь просит ссылку, данные из базы знаний или research.\n"+
			"Не отвечай, что инструмента нет: он есть в списке tools. Если инструмент вернул ошибку, скажи о ней.\n"+
			"Факты и ссылки из инструментов помечены как mock.",
		strings.Join(names, ", "),
	)
}

func appendDisplay(answer, display string) string {
	if display == "" || strings.Contains(answer, display) {
		return answer
	}
	if answer = strings.TrimSpace(answer); answer != "" {
		answer += "\n\n"
	}
	return answer + display
}

func toolMessage(name string, res entity.Result) entity.Message {
	raw, err := json.Marshal(res)
	if err != nil {
		raw = []byte(`{"error":"tool result is not JSON"}`)
	}
	return entity.Message{Role: "tool", Name: name, Content: string(raw)}
}

func lastUser(msgs []entity.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i].Content
		}
	}
	return ""
}

func callKey(tc entity.ToolCall) string {
	raw, err := json.Marshal(tc.Args)
	if err != nil {
		return tc.Name
	}
	return tc.Name + ":" + string(raw)
}

func fatal(err error) bool {
	return errors.Is(err, entity.ErrUnauthorized) ||
		errors.Is(err, entity.ErrInternal) ||
		errors.Is(err, entity.ErrConfig)
}

func publicError(err error) string {
	switch {
	case errors.Is(err, entity.ErrNotFound):
		return "tool not found"
	case errors.Is(err, entity.ErrBudget):
		return "budget exceeded"
	case errors.Is(err, entity.ErrUpstream):
		return "upstream unavailable"
	case errors.Is(err, entity.ErrInvalid):
		return "invalid arguments"
	default:
		return "tool failed"
	}
}
