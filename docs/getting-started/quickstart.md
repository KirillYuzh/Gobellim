# Быстрый старт

kit — библиотека. Приложение создаёт `App`, один раз выполняет `Setup`, регистрирует
инструменты и вызывает операции, положив принципала в контекст.

`kit.New` проверяет конфигурацию и не делает сетевых вызовов. Индексы создаёт
`app.Setup`.

## Когда использовать

- Нужен минимальный каркас: чат с инструментами и поиск по документам в одном процессе.
- У вас уже есть клиент Elasticsearch и клиент LLM.
- Нужно увидеть, куда kit встраивается в обычный Go-сервис.

## Быстрый старт

`main.go`

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/gobellim/kit"
	"github.com/gobellim/kit/authz"
	"github.com/gobellim/kit/model"
)

type weatherIn struct {
	City string `json:"city"`
}

func main() {
	ctx := context.Background()

	policy, err := authz.Load("policy.yaml")
	if err != nil {
		log.Fatal(err)
	}
	llm, err := model.NewOpenAI(model.OpenAIConfig{
		BaseURL: "http://localhost:8000/v1", // vLLM, Ollama или OpenAI-совместимый API
		Model:   "Qwen2.5-7B-Instruct",
	})
	if err != nil {
		log.Fatal(err)
	}

	app, err := kit.New(ctx, kit.Config{
		ES:     es, // ваш клиент Elasticsearch, интерфейс store.ES (см. ниже)
		LLM:    llm,
		Policy: policy,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = app.Close(context.Background()) }()

	if err := app.Setup(ctx); err != nil { // создаёт индексы один раз
		log.Fatal(err)
	}

	if _, err := kit.Tool(app, "get_weather", getWeather, kit.Desc("Текущая погода в городе.")); err != nil {
		log.Fatal(err)
	}

	ctx = kit.WithPrincipal(ctx, kit.Principal{UserID: "u-1", Roles: []string{"user"}, Tenant: "acme"})

	answer, err := app.Chat(ctx, kit.ChatRequest{
		System:   "Ты ассистент поддержки. Отвечай кратко.",
		Messages: []model.Message{{Role: model.RoleUser, Content: "Какая погода в Москве?"}},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(answer.Content)
}

func getWeather(_ context.Context, in weatherIn) (string, error) {
	return fmt.Sprintf("В городе %s солнечно, 12 °C", in.City), nil
}
```

## Как использовать

### Принципал

Каждая операция требует принципала в контексте. Его кладёт `kit.WithPrincipal`:

```go
ctx = kit.WithPrincipal(ctx, kit.Principal{UserID: "u-1", Roles: []string{"user"}, Tenant: "acme"})
```

Без принципала операция возвращает `ErrUnauthorized`. Права и ACL считаются по
`Roles` и `Tenant`.

### Инструменты

Инструмент — типизированная Go-функция. `kit.Tool[In, Out]` выводит JSON Schema из
типа `In`, сериализует результат `Out` для модели и валидирует аргументы по схеме.

```go
if _, err := kit.Tool(app, "get_weather", getWeather, kit.Desc("Текущая погода в городе.")); err != nil {
	return err
}
```

Право на инструмент по умолчанию — `tool:<name>`, то есть `tool:get_weather`. Его
можно задать явно опцией `kit.Perm`. Описание — `kit.Desc`.

### Клиент Elasticsearch

В примере выше `es` — ваш клиент. kit не поставляет клиент Elasticsearch (D10):
контракт — интерфейс `store.ES` с единственным методом `Do`. Минимальная реализация:

```go
type httpES struct {
	base string
	hc   *http.Client
}

func (c *httpES) Do(ctx context.Context, method, path string, body io.Reader) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		ct := "application/json"
		if strings.Contains(path, "_bulk") || strings.Contains(path, "_msearch") {
			ct = "application/x-ndjson"
		}
		req.Header.Set("Content-Type", ct)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return resp.StatusCode, data, err
}
```

### Поиск по документам

RAG — два вызова. `Ingest` режет документ на чанки, считает векторы и пишет их в ES.
`Search` выполняет BM25 и kNN под ACL принципала.

```go
err := app.Ingest(ctx, kit.Doc{ID: "policy-1", Text: text, ACL: []string{"user"}})
if err != nil {
	return err
}

hits, err := app.Search(ctx, kit.SearchRequest{Query: "политика возврата", TopK: 5})
if err != nil {
	return err
}
```

`Ingest` и `Search` требуют `Embedder` в `kit.Config` — без него они возвращают
`ErrInvalidConfig`. ACL документа считается при записи и хранится в каждом чанке.

### policy.yaml

Права задаются в `policy.yaml`. Минимум для примера:

```yaml
version: 1
default: deny

roles:
  user:
    permissions:
      - search
      - ingest
      - tool:get_weather
```

`authz.Load(path)` читает файл строгим декодером: неизвестные поля — ошибка,
`version` должен быть `1`, `default` — `deny`. Права на операции — `search`,
`ingest`, `delete`, `trace`; право на инструмент — `tool:<name>`. `*` разрешает всё.
За основу можно взять `demo/policy.yaml`.

### Живой пример

Поднимите стек, затем запустите бота из корня репозитория:

```sh
cd deploy && docker compose up -d elasticsearch grafana
```

```sh
go run ./demo/cmd/bot
```

Демо использует GigaChat, поэтому нужен `GIGACHAT_CREDENTIALS`. Сервис слушает
`:8080` и после готовности пишет `bot listening`. Полное описание ручек и переменных
окружения — в [demo/README.ru.md](../../demo/README.ru.md).

## Дальше

| Цель                         | Куда идти                               |
| ---------------------------- | --------------------------------------- |
| Дать модели свои инструменты | [Инструменты](../concepts/tools.md)     |
| Загрузить документы и искать | [Поиск](../concepts/retrieval.md)       |
| Разрешить доступ по ролям    | [Доступ](../concepts/access-control.md) |
| Подключить свою модель       | [Провайдеры](../reference/providers.md) |

## Смотрите также

- [Chat](../concepts/chat.md) — цикл «модель → инструмент → модель»
- [Справочник kit](../reference/kit.md) — все поля `Config`, `ChatRequest`, `SearchRequest`
- [Ошибки](../reference/errors.md) — sentinel-ошибки и что они значат
