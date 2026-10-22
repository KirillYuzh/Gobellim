package usecase

import (
	"context"
	"fmt"
	"strings"

	"some-project/internal/entity"
	"some-project/internal/workflow"
)

// researchIn — аргументы сценария. Ветку выбирает модель.
type researchIn struct {
	Query  string `json:"query" jsonschema:"description=Тема исследования"`
	Branch string `json:"branch" jsonschema:"enum=a,enum=b,enum=other,enum=functions,enum=threads,description=Ветка: a — готовый факт, b — задать пользователю вопрос про функции или потоки, functions и threads — ответ на этот вопрос, other — ошибка upstream"`
}

// link — заглушка: модель получает только подтверждение, а ссылку сервис
// дописывает в конец ответа сам.
func link(context.Context, struct{}) (entity.Result, error) {
	return entity.Result{
		Body:   "Готово: ссылка добавлена в конец ответа.",
		Append: "https://example.invalid/go-doc (mock)",
	}, nil
}

// researchState — состояние сценария.
type researchState struct {
	Query  string
	Branch string
	Result entity.Result
}

// researchFlow: ветку выбирает модель, включая продолжение после вопроса.
var researchFlow = workflow.New[researchState]("research").
	Branch("route", researchRoute, map[string]*workflow.Flow[researchState]{
		"a":         workflow.New[researchState]("a").Then("answer", researchA),
		"b":         workflow.New[researchState]("b").Then("ask", researchAsk),
		"functions": workflow.New[researchState]("functions").Then("answer", researchFunctions),
		"threads":   workflow.New[researchState]("threads").Then("answer", researchThreads),
		"other":     workflow.New[researchState]("other").Then("report", researchOther),
	})

// researchRoute: готовый ответ пользователя в запросе важнее ветки, потому что
// модель часто оставляет branch=b на продолжении, а тему переносит в query.
func researchRoute(st *researchState) string {
	q := strings.ToLower(st.Query)
	switch {
	case strings.Contains(q, "функц"), strings.Contains(q, "function"):
		return "functions"
	case strings.Contains(q, "поток"), strings.Contains(q, "горутин"),
		strings.Contains(q, "goroutine"), strings.Contains(q, "thread"):
		return "threads"
	case st.Branch == "":
		return "a"
	}
	return st.Branch
}

func researchA(_ context.Context, st *researchState) error {
	st.Result = entity.Result{Code: "a", Body: "Mock-факт A: `go build ./...` собирает все пакеты модуля."}
	return nil
}

// researchAsk отдаёт вопрос пользователю. Ответ пользователя модель передаёт
// следующей веткой: functions или threads.
func researchAsk(_ context.Context, st *researchState) error {
	st.Result = entity.Result{
		Code: "b",
		Body: "Спроси у пользователя, что именно он хочет узнать: про функции или про потоки?",
	}
	return nil
}

func researchFunctions(_ context.Context, st *researchState) error {
	st.Result = entity.Result{Code: "b", Body: "Mock-ответ: функции в Go — значения первого класса, их можно передавать и возвращать."}
	return nil
}

func researchThreads(_ context.Context, st *researchState) error {
	st.Result = entity.Result{Code: "b", Body: "Mock-ответ: потоки в Go — это горутины, запускаются словом `go`."}
	return nil
}

func researchOther(_ context.Context, st *researchState) error {
	st.Result = entity.Result{Code: "other", Error: "mock-upstream вернул неизвестный код"}
	return nil
}

func (u *UseCase) research(ctx context.Context, in researchIn) (entity.Result, error) {
	if strings.TrimSpace(in.Query) == "" {
		return entity.Result{}, fmt.Errorf("query is required: %w", entity.ErrInvalid)
	}
	st := researchState{Query: in.Query, Branch: in.Branch}
	if err := researchFlow.Run(ctx, &st); err != nil {
		return entity.Result{}, err
	}
	return st.Result, nil
}
