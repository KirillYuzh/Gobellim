package usecase

import (
	"context"
	"fmt"
	"strings"

	"some-project/internal/entity"
	"some-project/internal/repository"
	"some-project/internal/workflow"
)

type queryIn struct {
	Query string `json:"query"`
}

// link — обычная заглушка: возвращает готовую ссылку, в сеть не ходит.
func link(context.Context, struct{}) (entity.Result, error) {
	return entity.Result{Display: "https://example.invalid/go-doc (mock)"}, nil
}

// researchState — состояние сценария. Mock в состоянии, чтобы шаги вызывали
// обычные функции, без обвязки инструментов.
type researchState struct {
	Mock   Mock
	Query  string
	KB     []entity.Chunk
	Lookup repository.Lookup
	Result entity.Result
}

// researchFlow: база знаний, upstream и ветка по коду ответа.
var researchFlow = workflow.New[researchState]("research").
	Then("kb", researchKB).
	Then("lookup", researchLookup).
	Branch("route", researchRoute, map[string]*workflow.Flow[researchState]{
		"a":     workflow.New[researchState]("a").Then("relay", researchRelay),
		"b":     workflow.New[researchState]("b").Then("ask", researchAsk),
		"other": workflow.New[researchState]("other").Then("report", researchReport),
	})

func researchKB(ctx context.Context, st *researchState) error {
	chunks, err := st.Mock.KB(ctx, st.Query)
	st.KB = chunks
	return err
}

func researchLookup(ctx context.Context, st *researchState) error {
	out, err := st.Mock.Lookup(ctx, st.Query)
	st.Lookup = out
	return err
}

func researchRoute(st *researchState) string {
	if st.Lookup.Code == "" {
		return "other"
	}
	return st.Lookup.Code
}

func researchRelay(_ context.Context, st *researchState) error {
	st.Result = entity.Result{Code: "a", Body: st.Lookup.Body, Data: st.KB}
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
	st := researchState{Mock: u.mock, Query: in.Query}
	if err := researchFlow.Run(ctx, &st); err != nil {
		return entity.Result{}, err
	}
	return st.Result, nil
}
