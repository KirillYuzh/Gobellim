# Eval

Eval — детерминированные проверки поверх записанных трейсов. Пакет `eval`
прогоняет набор кейсов через ваше приложение и проверяет полученный трейс:
совпал ли ответ, вызвала ли модель нужный инструмент, не было ли ошибок.

Это замена LLM-судье для регрессионных тестов: каждая проверка — чистая функция от
трейса и его observations, поэтому результат воспроизводим, ничего не стоит и не
требует сети (`D30`).

## Когда использовать

- Нужен регрессионный тест на поведение бота: инструментные вызовы, ответы, ошибки.
- Нужно ловить «поехавший» промпт или сломанный инструмент без ручной проверки.
- Нужны проверки в `go test` без базы, без модели и без ключей.

## Быстрый старт

Подключите `trace.NewRecorder()` как sink, прогоните кейсы через `eval.Run` и
сверьте результат.

```go
package eval_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/gobellim/kit"
	"github.com/gobellim/kit/eval"
	"github.com/gobellim/kit/model"
	"github.com/gobellim/kit/trace"
)

func TestBot(t *testing.T) {
	ctx := context.Background()
	ctx = kit.WithPrincipal(ctx, kit.Principal{
		UserID: "u-eval", Roles: []string{"user"}, Tenant: "acme",
	})

	rec := trace.NewRecorder()
	app, err := kit.New(ctx, kit.Config{
		ES: es, LLM: llm, Policy: policy,
		Sinks: []trace.Sink{rec}, // каждый документ попадает и в рекордер
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close(context.Background()) }()

	cases := []eval.Case{
		{
			Name:  "return-policy",
			Input: "Сколько дней даётся на возврат товара?",
			Checks: []eval.Check{
				eval.CalledTool("kb_search"),
				eval.NoToolErrors(),
				eval.Similarity("14 календарных дней", 0.5),
			},
		},
		{
			Name:  "small-talk",
			Input: "Расскажи анекдот.",
			Checks: []eval.Check{
				eval.UsedNoTools(),
				eval.Matches(regexp.MustCompile(`(?i)анекдот`)),
			},
		},
	}

	results := eval.Run(ctx, rec, cases, func(ctx context.Context, input string) error {
		_, err := app.Chat(ctx, kit.ChatRequest{
			System:   "Ты ассистент поддержки.",
			Messages: []model.Message{{Role: model.RoleUser, Content: input}},
		})
		return err
	})

	report := eval.Summarize(results)
	if !report.OK() {
		t.Fatal(report.String())
	}
}
```

`eval.Run` перед каждым кейсом вызывает `rec.Reset()`, поэтому кейс видит только
свой трейс. Кейсы выполняются последовательно; если `run` вернул ошибку, кейс
помечается упавшим, а проверки не считаются. `report.String()` печатает по строке
на кейс — `PASS name` или `FAIL name: ошибка`, без текста входа и выхода.

## Проверки

| Проверка                                | Что проверяет                                                |
| --------------------------------------- | ------------------------------------------------------------ |
| `Includes(s)` / `Excludes(s)`           | выход содержит / не содержит подстроку                       |
| `Equals(s)`                             | выход точно равен строке                                     |
| `Matches(re)`                           | выход совпал с `*regexp.Regexp`                              |
| `Similarity(s, min)`                    | Dice-близость выхода и `s` не ниже `min`                     |
| `CalledTool(name)` / `DidNotCall(name)` | инструмент вызван / не вызван                                |
| `UsedNoTools()`                         | инструментов не вызывали                                     |
| `MaxToolCalls(n)`                       | вызовов инструментов не больше `n`                           |
| `ToolOrder(a, b, …)`                    | имена встречаются в этом порядке (как подпоследовательность) |
| `NoToolErrors()`                        | ни один вызов инструмента не завершился ошибкой              |
| `All(...)` / `Any(...)` / `Not(...)`    | логические комбинации                                        |

Текстовые проверки смотрят только на `Trace.Output` (финальный ответ) и не читают
вход: во входе могут быть заглушки после маскирования. Проверки инструментов
читают «tool observations» — observations с `Kind == SPAN`; guard (`EVENT`) и
модель (`GENERATION`) за инструменты не считаются.

## Рекордер

`trace.NewRecorder()` — потокобезопасный in-memory `trace.Sink`. `rec.Snapshot()`
возвращает накопленные `Traces`, `Observations`, `Scores`. `eval.Recorded(rec)`
собирает последний трейс вместе с его observations — этим можно пользоваться и
вне пакета `eval`. Подробнее про sink и трейсы — [Наблюдаемость](./observability.md).

## Почему не LLM-судья

Проверки детерминированы: это подстрока, Dice-близость по биграммам и факт вызова
инструмента — чистые функции записанного трейса, поэтому прогон воспроизводим,
бесплатен и не ходит в сеть; LLM-судья не даёт ни того, ни другого (`D30`).

## Дальше

| Цель                             | Куда идти                           |
| -------------------------------- | ----------------------------------- |
| Понять модель трейсов и sink     | [Наблюдаемость](./observability.md) |
| Прогнать раунд через инструменты | [Chat](./chat.md)                   |
| Проверить, что выход проверяется | [Guardrails](./guardrails.md)       |

## Смотрите также

- [Наблюдаемость](./observability.md) — `trace.Recorder`, `Sinks`, sampling
- [Eval-наборы](../guides/evals.md) — регрессии по трейсам в `go test` и CI
- [Chat](./chat.md) — что записывается в трейс хода
- [Бизнес-правила](../internals/business-rules.md) — `BR-40`…`BR-47`
