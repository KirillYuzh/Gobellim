// Package usecase holds the scenarios: chat, followup and a direct tool call.
// It knows the model, the policy and the tool set; it knows nothing about HTTP.
package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"some-project/internal/entity"
	"some-project/internal/policy"
	"some-project/internal/repository"
	"some-project/internal/tools"
	"some-project/internal/workflow"
)

// LLM is the chat client the demo uses. Ollama implements it.
type LLM interface {
	Chat(ctx context.Context, req entity.CompletionRequest) (entity.Completion, error)
}

// Mock is the second service the tools call. repository.Mockd implements it.
type Mock interface {
	KB(ctx context.Context, query string) ([]entity.Chunk, error)
	Lookup(ctx context.Context, query string) (repository.Lookup, error)
	Followup(ctx context.Context, query, input string) (repository.Followup, error)
}

// Deps are the dependencies of the scenarios.
type Deps struct {
	LLM    LLM
	Mock   Mock
	Policy *policy.Policy
	Logger *slog.Logger
	Budget entity.Budget
}

// UseCase runs the scenarios.
type UseCase struct {
	llm    LLM
	mock   Mock
	policy *policy.Policy
	log    *slog.Logger
	budget entity.Budget
	tools  *tools.Set
}

// New validates the dependencies and compiles the tool set.
func New(d Deps) (*UseCase, error) {
	if d.LLM == nil || d.Mock == nil || d.Policy == nil {
		return nil, fmt.Errorf("usecase: LLM, mock and policy are required: %w", entity.ErrConfig)
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	u := &UseCase{
		llm:    d.LLM,
		mock:   d.Mock,
		policy: d.Policy,
		log:    d.Logger,
		budget: d.Budget.WithDefaults(),
	}
	set, err := u.buildTools()
	if err != nil {
		return nil, err
	}
	u.tools = set
	return u, nil
}

// Chat runs one dialogue turn.
func (u *UseCase) Chat(ctx context.Context, req entity.ChatRequest) (entity.ChatResponse, error) {
	s, err := u.session(req.User)
	if err != nil {
		return entity.ChatResponse{}, err
	}
	return s.chat(ctx, req.Messages)
}

// Followup is the second step of scenario B. It calls remote_followup through
// the same permission and budget path as the model's tool calls.
func (u *UseCase) Followup(ctx context.Context, req entity.FollowupRequest) (entity.ChatResponse, error) {
	s, err := u.session(req.User)
	if err != nil {
		return entity.ChatResponse{}, err
	}
	if err := s.authorize("chat"); err != nil {
		return entity.ChatResponse{}, err
	}
	res, err := s.CallTool(ctx, "remote_followup", map[string]any{"query": req.Query, "input": req.Input})
	if err != nil {
		return entity.ChatResponse{}, err
	}
	if res.Error != "" {
		return entity.ChatResponse{}, fmt.Errorf("followup: %s: %w", res.Error, entity.ErrUpstream)
	}
	return entity.ChatResponse{Status: "ok", Answer: res.Body}, nil
}

// CallTool invokes one tool directly, without the model.
func (u *UseCase) CallTool(ctx context.Context, user entity.User, name string, args map[string]any) (entity.Result, error) {
	s, err := u.session(user)
	if err != nil {
		return entity.Result{}, err
	}
	return s.CallTool(ctx, name, args)
}

// Health reports the service state and the compiled catalog.
func (u *UseCase) Health() entity.Health {
	return entity.Health{Status: "ok", Tools: u.tools.Names(), Budget: u.budget}
}

func (u *UseCase) session(user entity.User) (*Session, error) {
	if err := user.Validate(); err != nil {
		return nil, err
	}
	return &Session{uc: u, user: user}, nil
}

// Session is one user's view: it carries the principal and the tool-call
// budget of a single request.
type Session struct {
	uc    *UseCase
	user  entity.User
	calls int
}

// chat is the permission-checked model → tools → model loop.
func (s *Session) chat(ctx context.Context, in []entity.Message) (entity.ChatResponse, error) {
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
	for step := 0; step < s.uc.budget.MaxSteps; step++ {
		if err := ctx.Err(); err != nil {
			return entity.ChatResponse{ToolCalls: calls}, err
		}
		resp, err := s.uc.llm.Chat(ctx, entity.CompletionRequest{Messages: msgs, Tools: specs})
		if err != nil {
			return entity.ChatResponse{ToolCalls: calls}, fmt.Errorf("llm: %w: %w", err, entity.ErrUpstream)
		}
		last = resp.Content
		if len(resp.ToolCalls) == 0 {
			return entity.ChatResponse{Status: "ok", Answer: appendDisplay(resp.Content, display), ToolCalls: calls}, nil
		}
		if len(resp.ToolCalls) > s.uc.budget.MaxToolCallsPerStep {
			return entity.ChatResponse{Status: "budget", Answer: resp.Content, ToolCalls: calls}, nil
		}

		// A repeat inside one step is the model looping: do not run the side
		// effect twice and do not spend the budget.
		seen := make(map[string]bool, len(resp.ToolCalls))
		toolMsgs := make([]entity.Message, 0, len(resp.ToolCalls))
		for _, tc := range resp.ToolCalls {
			if seen[callKey(tc)] {
				toolMsgs = append(toolMsgs, toolMessage(tc.Name, entity.Result{Error: "duplicate tool call"}))
				continue
			}
			seen[callKey(tc)] = true

			res, err := s.call(ctx, tc)
			if err != nil {
				if fatal(err) {
					return entity.ChatResponse{ToolCalls: calls}, err
				}
				s.uc.log.Error("tool failed", "tool", tc.Name, "user", s.user.ID, "err", err)
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

// CallTool is the only path to a tool: permission, budget, argument size and
// schema, then the typed handler.
func (s *Session) CallTool(ctx context.Context, name string, args map[string]any) (entity.Result, error) {
	return s.call(ctx, entity.ToolCall{Name: name, Args: args})
}

func (s *Session) call(ctx context.Context, tc entity.ToolCall) (entity.Result, error) {
	def := s.uc.tools.Get(tc.Name)
	if def == nil {
		return entity.Result{}, entity.ErrNotFound
	}
	if err := s.authorize(def.Permission()); err != nil {
		return entity.Result{}, err
	}
	if s.calls >= s.uc.budget.MaxToolCalls {
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
	if len(raw) > s.uc.budget.MaxArgsBytes {
		return entity.Result{}, fmt.Errorf("tool args too large: %w", entity.ErrBudget)
	}

	s.uc.log.Info("tool", "name", def.Name(), "effect", def.Effect().String(), "user", s.user.ID)
	out, err := def.Invoke(withSession(ctx, s), raw)
	if err != nil {
		return entity.Result{}, err
	}
	var res entity.Result
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		return entity.Result{}, fmt.Errorf("tool result: %w: %w", err, entity.ErrInternal)
	}
	return res, nil
}

func (s *Session) prepare(in []entity.Message) ([]entity.Message, error) {
	if len(in) == 0 || len(in) > s.uc.budget.MaxMessages {
		return nil, fmt.Errorf("message count must be in [1, %d]: %w", s.uc.budget.MaxMessages, entity.ErrInvalid)
	}
	out := make([]entity.Message, 0, len(in))
	hasUser, total := false, 0
	for _, m := range in {
		if m.Role != "user" && m.Role != "assistant" {
			continue // the client cannot inject a system prompt or a tool result
		}
		hasUser = hasUser || m.Role == "user"
		total += len(m.Content)
		if total > s.uc.budget.MaxHistoryBytes {
			return nil, fmt.Errorf("history is too large: %w", entity.ErrInvalid)
		}
		out = append(out, entity.Message{Role: m.Role, Content: m.Content})
	}
	if !hasUser {
		return nil, fmt.Errorf("at least one user message is required: %w", entity.ErrInvalid)
	}
	return out, nil
}

func (s *Session) specs() []entity.ToolSpec {
	out := make([]entity.ToolSpec, 0, s.uc.tools.Len())
	for _, def := range s.uc.tools.All() {
		if !s.uc.policy.Allows(s.user, def.Permission()) {
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

func (s *Session) authorize(permission string) error {
	if s.uc.policy.Allows(s.user, permission) {
		return nil
	}
	return fmt.Errorf("%s: %w", permission, entity.ErrUnauthorized)
}

// --- tools ---

type queryIn struct {
	Query string `json:"query"`
}

type followupIn struct {
	Query string `json:"query"`
	Input string `json:"input"`
}

// buildTools declares the catalog. The permission is tool:<name> unless a tool
// names another one, so a policy grants each tool separately.
func (u *UseCase) buildTools() (*tools.Set, error) {
	return tools.NewRegistry().Register(
		tools.New("link", u.link,
			tools.Desc("Добавить заготовленную mock-ссылку в конец ответа."), tools.Read()),
		tools.New("kb_search", u.kbSearch,
			tools.Desc("Поиск по mock-базе знаний о Go. Возвращает чанки со score."), tools.Read()),
		tools.New("remote_lookup", u.remoteLookup,
			tools.Desc("Запрос к mock-сервису. Возвращает код a, b или other."), tools.Egress()),
		tools.New("remote_followup", u.remoteFollowup,
			tools.Desc("Второй запрос к mock-сервису с уточнением пользователя."),
			tools.Egress(), tools.Perm("tool:remote_lookup")),
		tools.New("research", u.research,
			tools.Desc("Сценарий: база знаний, затем mock-upstream, затем ветка по коду ответа."),
			tools.Egress()),
	).Build()
}

func (u *UseCase) link(context.Context, struct{}) (entity.Result, error) {
	return entity.Result{Display: "https://example.invalid/go-doc (mock)"}, nil
}

func (u *UseCase) kbSearch(ctx context.Context, in queryIn) (entity.Result, error) {
	if err := requireQuery(in.Query); err != nil {
		return entity.Result{}, err
	}
	chunks, err := u.mock.KB(ctx, in.Query)
	if err != nil {
		return entity.Result{}, err
	}
	return entity.Result{Data: chunks}, nil
}

func (u *UseCase) remoteLookup(ctx context.Context, in queryIn) (entity.Result, error) {
	if err := requireQuery(in.Query); err != nil {
		return entity.Result{}, err
	}
	out, err := u.mock.Lookup(ctx, in.Query)
	if err != nil {
		return entity.Result{}, err
	}
	switch out.Code {
	case "a":
		return entity.Result{Code: "a", Body: out.Body}, nil
	case "b":
		return entity.Result{Code: "b", Question: out.Question, NeedInput: true}, nil
	default:
		return entity.Result{Code: "other", Error: "upstream returned an unknown code"}, nil
	}
}

func (u *UseCase) remoteFollowup(ctx context.Context, in followupIn) (entity.Result, error) {
	if err := requireQuery(in.Query); err != nil {
		return entity.Result{}, err
	}
	if strings.TrimSpace(in.Input) == "" {
		return entity.Result{}, fmt.Errorf("input is required: %w", entity.ErrInvalid)
	}
	out, err := u.mock.Followup(ctx, in.Query, in.Input)
	if err != nil {
		return entity.Result{}, err
	}
	if out.Error != "" {
		return entity.Result{Error: "upstream error"}, nil
	}
	return entity.Result{Body: out.Body}, nil
}

// --- workflow ---

// researchState is the typed state of the research scenario.
type researchState struct {
	Session *Session
	Query   string
	KB      entity.Result
	Lookup  entity.Result
	Result  entity.Result
}

// researchFlow is the deterministic scenario: kb_search, remote_lookup, then
// one branch by the upstream code. Every step calls back through Session, so
// permission, budget and schema validation still apply.
var researchFlow = workflow.New[researchState]("research").
	Then("kb", researchKB).
	Then("lookup", researchLookup).
	Branch("route", researchRoute, map[string]*workflow.Flow[researchState]{
		"a":     workflow.New[researchState]("a").Then("relay", researchRelay),
		"b":     workflow.New[researchState]("b").Then("ask", researchAsk),
		"other": workflow.New[researchState]("other").Then("report", researchReport),
	})

func researchKB(ctx context.Context, st *researchState) error {
	res, err := st.Session.CallTool(ctx, "kb_search", map[string]any{"query": st.Query})
	st.KB = res
	return err
}

func researchLookup(ctx context.Context, st *researchState) error {
	res, err := st.Session.CallTool(ctx, "remote_lookup", map[string]any{"query": st.Query})
	st.Lookup = res
	return err
}

func researchRoute(st *researchState) string {
	if st.Lookup.Code == "" {
		return "other"
	}
	return st.Lookup.Code
}

func researchRelay(_ context.Context, st *researchState) error {
	st.Result = entity.Result{Code: "a", Body: st.Lookup.Body, Data: st.KB.Data}
	return nil
}

func researchAsk(_ context.Context, st *researchState) error {
	st.Result = entity.Result{Code: "b", Question: st.Lookup.Question, NeedInput: true}
	return nil
}

func researchReport(_ context.Context, st *researchState) error {
	msg := st.Lookup.Error
	if msg == "" {
		msg = "upstream returned an unknown code"
	}
	st.Result = entity.Result{Code: "other", Error: msg}
	return nil
}

func (u *UseCase) research(ctx context.Context, in queryIn) (entity.Result, error) {
	if err := requireQuery(in.Query); err != nil {
		return entity.Result{}, err
	}
	s := sessionFrom(ctx)
	if s == nil {
		return entity.Result{}, fmt.Errorf("research: no session: %w", entity.ErrInternal)
	}
	st := researchState{Session: s, Query: in.Query}
	if err := researchFlow.Run(ctx, &st); err != nil {
		return entity.Result{}, err
	}
	return st.Result, nil
}

// --- helpers ---

type sessionKey struct{}

func withSession(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, sessionKey{}, s)
}

func sessionFrom(ctx context.Context) *Session {
	s, _ := ctx.Value(sessionKey{}).(*Session)
	return s
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

func requireQuery(q string) error {
	if strings.TrimSpace(q) == "" {
		return fmt.Errorf("query is required: %w", entity.ErrInvalid)
	}
	return nil
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
