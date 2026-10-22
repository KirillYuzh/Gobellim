package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"some-project/internal/entity"
	"some-project/internal/session"
)

// startIn — запуск исследования. Param приходит, если пользователь назвал
// параметр P сразу.
type startIn struct {
	Query string `json:"query" jsonschema:"description=Тема исследования"`
	Param string `json:"param,omitempty" jsonschema:"description=Параметр P, если пользователь назвал его сразу"`
}

// researchState — состояние сценария. Переходы считаются по этим полям, ветку
// выбирает код, а не модель.
type researchState struct {
	Query  string
	Param  string
	Path   string
	Topic  string
	Result string
}

// newResearch объявляет сценарий: параметр P, путь A или B, а в пути B ещё один
// вопрос. Вопросы, ответы, хранение запусков и пауза живут в пакете session.
func newResearch() *session.Session[researchState] {
	return session.New[researchState]("research").
		AskText("param", "Какой параметр P использовать в исследовании?",
			func(st *researchState, answer string) error {
				st.Param = answer
				return nil
			},
			session.SkipIf(func(st *researchState) bool { return st.Param != "" })).
		AskChoice("path", "Что сделать: путь A (готовый текст) или путь B (вопрос и уточнение)?",
			[]string{"A", "B"},
			func(st *researchState, answer string) error {
				st.Path = answer
				return nil
			}).
		Branch(func(st *researchState) string { return st.Path }).
		Case("A", func(f *session.Session[researchState]) {
			f.Do("finish_a", func(_ context.Context, st *researchState) error {
				st.Result = "Путь A: заготовленный текст исследования по «" + st.Param + "»."
				return nil
			})
		}).
		Case("B", func(f *session.Session[researchState]) {
			f.AskChoice("topic", "Что показать по теме?", []string{"X", "Y"},
				func(st *researchState, answer string) error {
					st.Topic = answer
					return nil
				}).
				Do("finish_b", func(_ context.Context, st *researchState) error {
					if st.Topic == "X" {
						st.Result = "Ответ X: пример кода по «" + st.Param + "»."
						return nil
					}
					st.Result = "Ответ Y: объяснение по «" + st.Param + "» своими словами."
					return nil
				})
		})
}

// link — заглушка: модель получает только подтверждение, а ссылку сервис
// дописывает в конец ответа сам.
func link(context.Context, struct{}) (entity.Result, error) {
	return entity.Result{
		Body:   "Готово: ссылка добавлена в конец ответа.",
		Append: "https://example.invalid/go-doc (mock)",
	}, nil
}

func (u *UseCase) startResearch(ctx context.Context, in startIn) (entity.Result, error) {
	query := strings.TrimSpace(in.Query)
	if query == "" {
		return entity.Result{}, fmt.Errorf("query is required: %w", entity.ErrInvalid)
	}
	user, ok := entity.UserFrom(ctx)
	if !ok {
		return entity.Result{}, fmt.Errorf("research: no principal: %w", entity.ErrInternal)
	}
	res, err := u.research.Start(ctx, principal(user), &researchState{
		Query: query,
		Param: strings.TrimSpace(in.Param),
	})
	if err != nil {
		return entity.Result{}, mapSessionError(err)
	}
	return toToolResult(res), nil
}

// toToolResult переводит результат сценария в результат инструмента. Модель
// видит только статус и вопрос, RunID вырезает агент.
func toToolResult(res session.Result[researchState]) entity.Result {
	out := entity.Result{Status: string(res.Status), RunID: res.RunID}
	if res.Question != nil {
		out.Prompt = res.Question.Prompt
		out.Input = string(res.Question.Input)
		out.Options = res.Question.Options
		return out
	}
	out.Body = res.State.Result
	return out
}

// principal переводит пользователя демо в principal сценария.
func principal(u entity.User) session.Principal {
	return session.Principal{UserID: u.ID, Tenant: u.Tenant}
}

// mapSessionError переводит ошибки сценария в доменные: чужой запуск отдаётся
// как «не найдено», кривой ответ — как некорректный запрос.
func mapSessionError(err error) error {
	switch {
	case errors.Is(err, session.ErrNotFound):
		return fmt.Errorf("%w: %w", err, entity.ErrNotFound)
	case errors.Is(err, session.ErrInvalid):
		return fmt.Errorf("%w: %w", err, entity.ErrInvalid)
	default:
		return err
	}
}
