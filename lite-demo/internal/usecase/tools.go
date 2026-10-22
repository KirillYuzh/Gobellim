package usecase

import (
	"context"
	"fmt"
	"strings"

	"some-project/internal/agent"
	"some-project/internal/entity"
	"some-project/internal/workflow"
)

type queryIn struct {
	Query string `json:"query"`
}

type followupIn struct {
	Query string `json:"query"`
	Input string `json:"input"`
}

// link — заглушка: возвращает готовую ссылку, не ходит в сеть.
func link(context.Context, struct{}) (entity.Result, error) {
	return entity.Result{Display: "https://example.invalid/go-doc (mock)"}, nil
}

func (u *UseCase) kbSearch(ctx context.Context, in queryIn) (entity.Result, error) {
	chunks, err := u.mock.KB(ctx, in.Query)
	if err != nil {
		return entity.Result{}, err
	}
	return entity.Result{Data: chunks}, nil
}

func (u *UseCase) remoteLookup(ctx context.Context, in queryIn) (entity.Result, error) {
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
	out, err := u.mock.Followup(ctx, in.Query, in.Input)
	if err != nil {
		return entity.Result{}, err
	}
	if out.Error != "" {
		return entity.Result{Error: "upstream error"}, nil
	}
	return entity.Result{Body: out.Body}, nil
}

// researchState — состояние сценария research.
type researchState struct {
	Session *agent.Session
	Query   string
	KB      entity.Result
	Lookup  entity.Result
	Result  entity.Result
}

// researchFlow: база знаний, upstream и ветка по коду ответа. Каждый шаг
// вызывает инструмент через сессию, поэтому права и бюджет те же.
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
	if strings.TrimSpace(in.Query) == "" {
		return entity.Result{}, fmt.Errorf("query is required: %w", entity.ErrInvalid)
	}
	s := agent.SessionFrom(ctx)
	if s == nil {
		return entity.Result{}, fmt.Errorf("research: no session: %w", entity.ErrInternal)
	}
	st := researchState{Session: s, Query: in.Query}
	if err := researchFlow.Run(ctx, &st); err != nil {
		return entity.Result{}, err
	}
	return st.Result, nil
}
