# lite-demo

Минимальный сервис на Go, который показывает, как устроены инструменты и
детерминированные workflow вокруг LLM. Это самостоятельный пример: у него своя
реализация движка инструментов, свой workflow и свой клиент GigaChat. Пакеты
`github.com/KirillYuzh/Gobellim` в зависимостях нет.

Демо не про безопасность: guard, gate и trace здесь намеренно отсутствуют.
Осталось то, что показывает сценарий: principal, policy, инструменты, бюджет и
workflow.

## Сценарий

Модель отвечает на вопрос и может вызвать инструмент:

| Инструмент | Право           | Что делает                            |
| ---------- | --------------- | ------------------------------------- |
| `link`     | `tool:link`     | дописывает mock-ссылку в конец ответа |
| `research` | `tool:research` | mock-workflow с ветками               |

`research` — workflow, ветку которого выбирает модель аргументом `branch`:

- `a` — готовый факт;
- `b` — вопрос пользователю: про функции или про потоки;
- `other` — ошибка upstream.

После ответа пользователя сценарий продолжается: модель кладёт ответ в `query`,
и ветка становится `functions` или `threads`. Все данные — заглушки, внешних
сервисов нет.

## Один путь

Каждый инструмент, который назвала модель, идёт через одну функцию
`session.call`: право → бюджет → схема аргументов → обработчик. Шаги `research`
— обычные функции, вложенных инструментов нет.

```mermaid
sequenceDiagram
    participant C as Клиент
    participant H as controller
    	participant U as agent.session
    participant L as GigaChat
    participant T as tools.Set

    C->>H: POST /v1/chat
    H->>U: Chat(messages)
    U->>U: policy chat
    U->>L: сообщения + разрешённые tools
    L-->>U: tool_calls
    U->>U: policy tool, бюджет, схема
    U->>T: Invoke
    T-->>U: результат
    U->>L: tool-сообщение
    L-->>U: ответ
    U-->>H: Answer
    H-->>C: JSON
```

## Быстрый старт

Нужен Docker с BuildKit:

```sh
docker compose up -d --build
```

Ключ `GIGACHAT_API_KEY` лежит в `lite-demo/.env`; compose передаёт его
контейнеру через `env_file`.

Запрос:

```sh
curl -s http://localhost:8080/v1/chat \
  -H 'Content-Type: application/json' \
  -d '{
    "user": {"id": "u-1", "tenant": "acme", "roles": ["support"]},
    "messages": [{"role": "user", "content": "Сделай research по go modules, ветка a"}]
  }'
```

## Локально

Нужен только ключ `GIGACHAT_API_KEY` в `lite-demo/.env`:

```sh
set -a; . ./.env; set +a
go run ./cmd/lite-demo
```

## API

`POST /v1/chat`, `GET /healthz`.

Статусы чата: `ok`, `max_steps`, `budget`.

| Код | Когда                                     |
| --- | ----------------------------------------- |
| 400 | битый JSON, лишние поля, нет пользователя |
| 403 | policy запретила действие                 |
| 502 | не ответил GigaChat                       |
| 504 | истёк дедлайн запроса                     |
| 500 | внутренняя ошибка                         |

Текст ошибки всегда безопасный: детали upstream и policy остаются в логе.

## Права

`policy.json` — deny by default:

```json
{
  "default": "deny",
  "roles": {
    "user": ["chat", "tool:link"],
    "support": ["chat", "tool:link", "tool:research"]
  }
}
```

Права проверяются дважды: `specs` не показывает модели инструмент, на который у
роли нет права, а `call` повторяет проверку перед запуском. Поэтому модель не
видит запрещённый инструмент, а если придумает его сама — получит отказ.

В демо principal приходит в теле запроса. В проде здесь JWT/OIDC на границе
`controller`, а usecase не меняется.

## Инструменты

`internal/tools` — типизированный движок. Схема аргументов выводится из `In`,
результат проверяется по схеме `Out`, а права по умолчанию — `tool:<name>`:

```go
tools.New("link", link,
	tools.Desc("Добавить ссылку в конец ответа. Приложение сделает это само."),
	tools.Read(),
)

tools.New("research", u.research,
	tools.Desc("Mock-сценарий. Ветки: a — факт, b — вопрос, functions и threads — ответ, other — ошибка."),
	tools.Read(),
)
```

Ветку `branch` модель выбирает сама: в схеме это enum, а не свободная строка.

`Registry` собирает каталог один раз; ошибка объявления и повтор имени видны в
`Build`, а не в рантайме.

## Workflow

`internal/workflow` — типизированный state machine. Шаг получает `*S` и меняет
его явно, поэтому опечатка в поле — ошибка компиляции. Ветка выбирает
под-workflow по состоянию.

```go
var researchFlow = workflow.New[researchState]("research").
	Branch("route", researchRoute, map[string]*workflow.Flow[researchState]{
		"a":         workflow.New[researchState]("a").Then("answer", researchA),
		"b":         workflow.New[researchState]("b").Then("ask", researchAsk),
		"functions": workflow.New[researchState]("functions").Then("answer", researchFunctions),
		"threads":   workflow.New[researchState]("threads").Then("answer", researchThreads),
		"other":     workflow.New[researchState]("other").Then("report", researchOther),
	})

func researchAsk(_ context.Context, st *researchState) error {
	st.Result = entity.Result{Code: "b", Body: "Спроси у пользователя..."}
	return nil
}
```

Ошибка шага останавливает цепочку и возвращается с путём `research/ask`. `Run`
проверяет циклы и дубли имён, ограничивает глубину и число шагов, ловит панику
шага, а паника observer возвращает `ErrObserver`.

## Структура

| Путь                  | Что                                                        |
| --------------------- | ---------------------------------------------------------- |
| `cmd/lite-demo`       | точка входа сервиса                                        |
| `internal/app`        | сборка зависимостей, сервер, graceful shutdown             |
| `internal/agent`      | системная часть: цикл модель → инструменты, права и бюджет |
| `internal/config`     | окружение и проверки старта                                |
| `internal/controller` | HTTP-граница: разбор запроса, вызов usecase, ответ         |
| `internal/entity`     | типы домена и ошибки-сентинелы                             |
| `internal/errmap`     | ошибка → HTTP-код и безопасное сообщение                   |
| `internal/llm`        | клиент GigaChat `/v1/chat/completions`                     |
| `internal/policy`     | роли и права, deny by default                              |
| `internal/routes`     | таблица маршрутов и recover                                |
| `internal/tools`      | движок инструментов: схема, каталог, вызов                 |
| `internal/usecase`    | сценарии: чат, инструменты и health                        |
| `internal/workflow`   | типизированный workflow с ветками                          |
| `pkg/rest`            | JSON-ответ и разбор тела запроса                           |

## Настройки

| Переменная          | По умолчанию                                        | Зачем                                  |
| ------------------- | --------------------------------------------------- | -------------------------------------- |
| `APP_ENV`           | dev                                                 | `prod` отклоняется                     |
| `HTTP_ADDR`         | `:8080`                                             | адрес сервера                          |
| `POLICY_PATH`       | `policy.json`                                       | файл прав                              |
| `GIGACHAT_API_KEY`  | из `.env`                                           | OAuth-ключ, обязателен                 |
| `GIGACHAT_MODEL`    | `GigaChat-2`                                        | модель                                 |
| `GIGACHAT_BASE_URL` | `https://api.giga.chat`                             | API                                    |
| `GIGACHAT_AUTH_URL` | `https://ngw.devices.sberbank.ru:9443/api/v2/oauth` | OAuth                                  |
| `GIGACHAT_SCOPE`    | `GIGACHAT_API_PERS`                                 | скоуп OAuth                            |
| `REQUEST_TIMEOUT`   | `3m`                                                | дедлайн одного запроса                 |
| `LOG_LEVEL`         | `info`                                              | `debug` печатает запрос и ответ модели |

### Что получила модель

На уровне `debug` сервис печатает каждый запрос к GigaChat и ответ на него:
системный промпт, историю, схемы функций и результаты инструментов. Включите
его в `.env` и перезапустите стек:

```sh
echo 'LOG_LEVEL=debug' >> .env
docker compose up -d

# только тела запросов, без обёртки лога
docker compose logs app \
  | jq -r 'select(.msg == "llm request") | .body | fromjson'
```

## Проверки

```sh
go test -race -shuffle=on ./...
go vet ./...
golangci-lint run
gofumpt -l .
```
