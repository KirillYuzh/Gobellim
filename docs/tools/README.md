# Обзор

Инструмент — типизированная Go-функция `func(ctx context.Context, in In) (Out, error)`,
которую модель может вызвать. Объявление чистое: оно не трогает `App`, а ошибки
видны при сборке каталога.

## Объявление

```go
type weatherIn struct {
	City string `json:"city"`
}

type weatherOut struct {
	Temp int    `json:"temp"`
	Unit string `json:"unit"`
}

getWeather := gobellim.Tool("get_weather",
	func(ctx context.Context, in weatherIn) (weatherOut, error) {
		return weatherOut{Temp: 12, Unit: "C"}, nil
	},
	gobellim.Desc("Текущая погода в городе"),
)
```

Схема аргументов выводится из `In` автоматически (через `invopop/jsonschema`),
результат сериализуется из `Out` и проверяется по его схеме. Для
`map[string]string`, `json.RawMessage` и `any` схема остаётся открытой.

## Регистрация

```go
tools := gobellim.NewRegistry().Register(
	getWeather,
	gobellim.RetrievalTool("search_knowledge"),
)
toolSet, err := tools.Build()
if err != nil {
	return err
}

app, err := gobellim.New(ctx, gobellim.Config{
	LLM:    llm,
	Policy: policy,
	Tools:  toolSet,
})
```

`Build` возвращает сразу все ошибки объявлений (`errors.Join`): пустое имя, nil
обработчик, повтор имени, некорректная policy.

## Эффекты

Политика инструмента — это `Option`, поэтому её передают прямо в `Tool`:

```go
// Только чтение — значение по умолчанию.
gobellim.Tool("get_weather", getWeather, gobellim.Desc("..."), gobellim.Read())

// Меняет состояние.
gobellim.Tool("create_order", createOrder, gobellim.Desc("..."), gobellim.Write())

// Отправляет данные наружу: нужен allowlist хостов.
gobellim.Tool("send_email", sendEmail,
	gobellim.Desc("..."),
	gobellim.Egress("api.example.com"),
)
```

Для эффекта без списка хостов и для обязательного подтверждения используйте
пакет `tool`:

```go
import "github.com/KirillYuzh/Gobellim/tool"

gobellim.Tool("send_email", sendEmail, tool.AnyHost())
gobellim.Tool("delete_user", deleteUser, tool.Approve())
```

Подтверждение требует `gobellim.Config.Approver`; без него вызов падает с
`ErrApprovalRequired`.

## Права

По умолчанию право — `tool:<name>` (`tool:get_weather`). Переопределяется:

```go
gobellim.Tool("get_weather", getWeather, gobellim.Perm("weather:get"))
```

Право проверяется дважды: запрещённый инструмент не попадает в список для
модели, и тот же запрет повторяется перед запуском.

## View

`View` задаёт, что увидит модель вместо сериализованного результата:

```go
gobellim.Tool("get_weather", getWeather,
	gobellim.View(func(_ context.Context, out weatherOut) string {
		return fmt.Sprintf("%d °%s", out.Temp, out.Unit)
	}),
)
```

## RawTool

Инструмент с внешней схемой, например из MCP:

```go
gobellim.RawTool("remote_tool", schema, func(ctx context.Context, args json.RawMessage) (string, error) {
	return callRemote(ctx, args)
})
```

Пустая схема становится `{"type":"object"}`.

## Ошибки

| Ошибка                | Когда                                   |
| --------------------- | --------------------------------------- |
| `ErrToolNotFound`     | имя не зарегистрировано                 |
| `ErrInvalidArgs`      | аргументы не JSON или не проходят схему |
| `ErrUnauthorized`     | policy не дала право                    |
| `ErrApprovalRequired` | нет подтверждения                       |
| `ErrInvalidConfig`    | объявление не собралось                 |

Некритичная ошибка внутри инструмента возвращается модели как результат, а не
роняет ход.

## Устройство

Как это устроено → [implementation.md](./implementation.md)

## Смотрите также

- [Быстрый старт](../overview.md)
- [Рабочие процессы](../workflows/README.md)
- [Справочник API](../reference/gobellim.md)
