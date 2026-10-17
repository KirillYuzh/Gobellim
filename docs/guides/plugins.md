# Плагины: как добавить свой детектор, sink или адаптер

Плагин в Gobellim — это реализация одного из портов библиотеки. Ядро не знает
про конкретный детектор, хранилище или провайдера: оно вызывает интерфейс.
Динамической загрузки `.so` нет. Плагин подключается на этапе компиляции: вы
реализуете интерфейс и передаёте значение в `kit.Config`.

| Порт           | Интерфейс         | Что делает                            | Куда подключается  |
| -------------- | ----------------- | ------------------------------------- | ------------------ |
| Детектор guard | `guard.Detector`  | находит PII, секреты, инъекции        | `Config.GuardL2`   |
| Sink трейсов   | `trace.Sink`      | принимает трейсы, наблюдения и оценки | `Config.Sinks`     |
| Хранилище      | `store.ES`        | выполняет запросы к индексу           | `Config.ES`        |
| Провайдер LLM  | `model.Client`    | `Chat` и `Model`                      | `Config.LLM`       |
| Эмбеддер       | `embed.Embedder`  | `Query`, `Passages`, `Dim`, `Model`   | `Config.Embedder`  |
| Реранкер       | `rerank.Reranker` | `Rerank` и `Name`                     | `Config.Reranker`  |
| Токенайзер     | `chunk.Tokenizer` | `Tokens`                              | `Config.Tokenizer` |

## Что поставляется и что нет

Референсная реализация одна: `guard/gliner` на ONNX Runtime, сборка
`-tags ort`. Он же пример плагина с моделью, конфигом и тестами.

`guard-remote`, `store-es` и `trace-otel` не реализованы (`D76`). OTLP-экспорт
встроен в `trace` (`trace.NewOTLP`), это sink, а не отдельный плагин.

`kittest` даёт контракты: `DetectorContract`, `SinkContract`, `ScopeIsolation`.
Контракт — это тест, а не обещание поддержки. Прогнать его стоит до PR.

## Требования

- Реализуйте интерфейс полностью. `Name()` должен быть непустым и стабильным: он
  попадает в `Health`, трейс и версию guard.
- Fail closed. Если `Detect` не может проверить текст, верните ошибку. По
  умолчанию pipeline останавливает операцию с `ErrGuardUnavailable`, а не
  пропускает текст.
- Не логируйте и не возвращайте сырой текст в ошибках. В сообщении — что
  сломалось, без фрагмента входа.
- `Finding.Start` и `Finding.End` — смещения в рунах, не в байтах. Маскирование
  режет `[]rune`, поэтому байтовые индексы на кириллице дадут неверный спан.
- `Detect` не меняет текст. Маскированием занимается pipeline.
- В этом репозитории тесты используют только стандартную библиотеку. Для
  детектора прогоните `kittest.DetectorContract`, для sink —
  `kittest.SinkContract`.
- Плагин не обязан жить в этом репозитории. Если вы добавляете его сюда, он
  должен быть отдельным модулем или за build tag: ядро не импортирует плагины.
  Правила импортов проверяет `internal/importrules`.
- Сетевой вызов внутри `Detect` — только с таймаутом и без бесконечных ретраев.
  Ошибка должна вернуться наверх, а не превратиться в «ничего не нашли».

## Пример с нуля: детектор по стоп-листу

Соберём `guard.Detector`, который отклоняет текст с запрещёнными терминами.
Модуль отдельный, зависит от `guard` и `kittest`.

```sh
mkdir blocklist && cd blocklist
go mod init example.com/blocklist
go get github.com/KirillYuzh/Gobellim@v0.1.0
go get github.com/KirillYuzh/Gobellim/kittest@v0.1.0
```

`blocklist.go`:

```go
package blocklist

import (
	"context"
	"unicode"

	"github.com/KirillYuzh/Gobellim/guard"
)

// Detector rejects text that contains one of the configured terms.
type Detector struct {
	name  string
	terms [][]rune
}

// New returns a detector with the given name and case-insensitive terms.
func New(name string, terms ...string) *Detector {
	d := &Detector{name: name}
	for _, term := range terms {
		if tr := []rune(term); len(tr) > 0 {
			d.terms = append(d.terms, tr)
		}
	}
	return d
}

// Name returns the layer name used in Health and traces.
func (d *Detector) Name() string { return d.name }

// Detect returns one CatUnsafe finding per term occurrence. Offsets are runes.
func (d *Detector) Detect(ctx context.Context, text string) ([]guard.Finding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	runes := []rune(text)
	var out []guard.Finding
	for _, term := range d.terms {
		for i := 0; i+len(term) <= len(runes); i++ {
			if !equalFold(runes[i:i+len(term)], term) {
				continue
			}
			out = append(out, guard.Finding{
				Category: guard.CatUnsafe,
				Label:    "BLOCKLIST",
				Start:    i,
				End:      i + len(term),
				Score:    1,
				Layer:    d.Name(),
			})
			i += len(term) - 1
		}
	}
	return out, nil
}

func equalFold(a, b []rune) bool {
	for i := range a {
		if unicode.ToLower(a[i]) != unicode.ToLower(b[i]) {
			return false
		}
	}
	return true
}
```

`blocklist_test.go`:

```go
package blocklist

import (
	"context"
	"testing"

	"github.com/KirillYuzh/Gobellim/kittest"
)

func TestContract(t *testing.T) {
	kittest.DetectorContract(t, New("blocklist", "internal-only"))
}

func TestDetectUsesRuneOffsets(t *testing.T) {
	fs, err := New("blocklist", "секрет").Detect(context.Background(), "это секрет")
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 {
		t.Fatalf("findings = %d, want 1", len(fs))
	}
	if fs[0].Start != 4 || fs[0].End != 10 {
		t.Fatalf("span = %d..%d, want 4..10", fs[0].Start, fs[0].End)
	}
}
```

Подключение:

```go
app, err := kit.New(ctx, kit.Config{
	LLM:     llm,
	Policy:  policy,
	GuardL2: blocklist.New("blocklist", "internal-only", "confidential"),
})
```

`GuardL2` принимает один детектор. Если нужен ваш детектор вместе с
`guard/gliner`, соберите композит:

```go
type multi struct {
	name string
	ds   []guard.Detector
}

func (m multi) Name() string { return m.name }

func (m multi) Detect(ctx context.Context, text string) ([]guard.Finding, error) {
	var out []guard.Finding
	for _, d := range m.ds {
		fs, err := d.Detect(ctx, text)
		if err != nil {
			return nil, err // fail closed
		}
		out = append(out, fs...)
	}
	return out, nil
}

app, err := kit.New(ctx, kit.Config{
	// ...
	GuardL2: multi{name: "L2", ds: []guard.Detector{glinerDetector, blocklist.New(...)}},
})
```

Если вы собираете `guard.Pipeline` сами, а не через `kit.New`, используйте
`guard.Options{Layers: ...}` или `guard.Ensemble{Name, Detectors}`.

## Sink трейсов

Sink получает три вида документов: трейс, наблюдение и оценку.

```go
type Sink struct {
	// ...
}

func (s *Sink) Enqueue(t trace.Trace)                     { /* ... */ }
func (s *Sink) EnqueueObservation(o trace.Observation)    { /* ... */ }
func (s *Sink) EnqueueScore(sc trace.Score)               { /* ... */ }
```

Подключение:

```go
app, err := kit.New(ctx, kit.Config{
	// ...
	Sinks: []trace.Sink{mySink},
})
```

`Enqueue` вызывается из writer'а конкурентно, поэтому sink должен быть
потокобезопасным. `kittest.SinkContract` проверяет это под `-race`.

## Готовые плагины

### guard/gliner

L2-детектор. Соберите с `-tags ort` и CGO:

```sh
CGO_ENABLED=1 go build -tags ort ./...
```

Модель лежит в `models/<model>/` (`model.onnx`, `tokenizer.json`,
`config.json`). Её готовит `tools/gliner/export.py`: в HF-репозитории лежит
`model.safetensors`, а не ONNX-граф.

```go
import "github.com/KirillYuzh/Gobellim/guard/gliner"

det, err := gliner.New(gliner.Config{
	Dir:   "models",
	Model: "hivetrace__gliner-guard-omni",
})
app, err := kit.New(ctx, kit.Config{
	// ...
	GuardL2: det,
})
```

Без `-tags ort` `gliner.New` возвращает заглушку, чей `Detect` падает с
`ErrGuardUnavailable`. Это fail-closed: заглушка не превращается в тихий L1-only.

### trace.NewOTLP

Встроенный sink, не отдельный плагин:

```go
sink, err := trace.NewOTLP(trace.OTLPOptions{
	Endpoint:    "http://localhost:4318",
	ServiceName: "my-service",
})
app, err := kit.New(ctx, kit.Config{
	// ...
	Sinks: []trace.Sink{sink},
})
```

### Хранилище

`Config.ES` — это `store.ES` с одним методом `Do`. Референсный HTTP-клиент есть
в `demo/internal/adapter/elastic/es.go`; его можно скопировать как основу.

Порт ES-образный: ядро шлёт `/_bulk`, `/_msearch` и `/_delete_by_query`. Для
pgvector или Qdrant `Do` должен транслировать эти запросы: одной реализации
метода недостаточно. Это ограничение контракта.

### Провайдеры и эмбеддеры

Готовые адаптеры и их поля — в [справочнике провайдеров](../reference/providers.md).
Свой клиент — это `model.Client` (`Chat`, `Model`) или `embed.Embedder`
(`Query`, `Passages`, `Dim`, `Model`). Эмбеддер может реализовать
`embed.Closer`, если держит ресурсы вроде ONNX-сессий.

## Куда смотреть в исходниках

| Что                           | Файлы                                                                                                                         |
| ----------------------------- | ----------------------------------------------------------------------------------------------------------------------------- |
| Интерфейсы портов             | `guard/guard.go`, `trace/sink.go`, `store/store.go`, `model/model.go`, `embed/embed.go`, `rerank/rerank.go`, `chunk/chunk.go` |
| Референсный детектор          | `guard/gliner/`                                                                                                               |
| Детектор для тестов           | `guard/guardtest/`                                                                                                            |
| Контракты плагинов            | `kittest/contract.go`                                                                                                         |
| Как фасад собирает слои guard | `guardwiring.go`, `kit.go` (`New`)                                                                                            |
| Путь вызова                   | `internal/call/`                                                                                                              |
| Пример store-адаптера         | `demo/internal/adapter/elastic/es.go`                                                                                         |

## Ограничения

- `guard-remote` не реализован. Удалённый guard — вне границ v1.
- Порт `store.ES` ES-образный; адаптер для другого хранилища транслирует запросы.
- `GuardL2` — один детектор. Несколько детекторов собираются композитом.
- Загрузки плагинов в рантайме нет: только compile-time.
- `kittest` — отдельный модуль; его тесты тоже используют только stdlib.

## Смотрите также

- [Guardrails](./guardrails.md) — слои, рельсы, деградация
- [Наблюдаемость](./observability.md) — sink'и и writer
- [Провайдеры](../reference/providers.md) — готовые `model.Client` и `embed.Embedder`
- [Конфигурация](../reference/configuration.md) — поля `Config`
- [Устройство: архитектура](../internals/architecture.md) — правила импортов и границы
