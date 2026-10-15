# Инструменты

Инструмент — типизированная Go-функция, которую модель вызывает по имени. kit выводит JSON Schema из типа аргументов, проверяет по ней вызов модели и исполняет функцию только после проверки прав.

## Когда использовать

- Модель должна получать данные, которых нет в промпте: из вашей системы, базы или внешнего API.
- Аргументы вызова нужно проверять по строгой схеме до исполнения.
- Вызов ограничивается правами роли.
- Результат функции — недоверенные данные, которые надо проверить guard'ом.

## Быстрый старт

Файл: `demo/internal/adapter/kitengine/tools.go`

```go
type weatherIn struct {
	City string `json:"city"`
}

type weatherOut struct {
	City         string  `json:"city"`
	TemperatureC float64 `json:"temperature_c"`
	WindKmh      float64 `json:"wind_kmh"`
	Description  string  `json:"description"`
}

func (e *Engine) getWeather(ctx context.Context, in weatherIn) (weatherOut, error) {
	w, err := e.weather.Current(ctx, in.City)
	if err != nil {
		return weatherOut{}, err
	}
	return weatherOut{
		City:         w.City,
		TemperatureC: w.TemperatureC,
		WindKmh:      w.WindKmh,
		Description:  w.Description,
	}, nil
}

func NewEngine(app *kit.App, weather Weather) (*Engine, error) {
	e := &Engine{app: app, weather: weather}
	if _, err := kit.Tool(app, "get_weather", e.getWeather, kit.Desc("Return the current weather for a city.")); err != nil {
		return nil, err
	}
	return e, nil
}
```

## Как использовать

### Сигнатура и регистрация

```go
func Tool[In, Out any](app *App, name string, fn func(ctx context.Context, in In) (Out, error), opts ...ToolOption) (*ToolDef, error)
```

- `In` задаёт аргументы, `Out` — результат; результат сериализуется в JSON и уходит модели.
- `Desc("...")` — описание для модели.
- `Perm("...")` — переопределить право; по умолчанию `tool:<name>`.
- `Tool` возвращает `ErrInvalidConfig`, если `app` равен nil, `name` пустое или `fn` равен nil.

### Схема из типа аргументов

JSON Schema выводится из `In` через `invopop/jsonschema` (`DoNotReference`, `ExpandedStruct`) при регистрации. Из схемы удаляются `$schema` и `$id`, и принудительно ставится `"additionalProperties": false`, поэтому лишние поля модель передать не может.

### Валидация аргументов

Аргументы модели валидируются по скомпилированной схеме (`santhosh-tekuri/jsonschema/v6`) до вызова функции. Невалидные аргументы — `ErrInvalidArgs`; на первый невалидный вызов даётся один ретрай, модель получает текст ошибки. Размер аргументов ограничен `ArgsMaxBytes` (по умолчанию 32 KB).

### Права и вычитание из схемы

Запрещённые по роли инструменты вычитаются из списка, который видит модель, и перепроверяются при исполнении. Решение принимает рантайм, а не модель: неизвестный или запрещённый инструмент даёт `ErrToolNotFound`.

### Данные из контекста

Инструмент берёт принципала из контекста. `kit.PrincipalFrom(ctx)` возвращает `(Principal, bool)`. Так `get_user_role` в demo узнаёт роль вызывающего, не принимая её аргументом.

```go
func (e *Engine) getUserRole(ctx context.Context, _ userRoleIn) (userRoleOut, error) {
	principal, ok := kit.PrincipalFrom(ctx)
	if !ok {
		return userRoleOut{}, fmt.Errorf("no principal in tool context: %w", domain.ErrUnauthorized)
	}
	return userRoleOut{Role: principal.Roles[0]}, nil
}
```

### Лимиты и guard

- `MaxCallsPerTool` (по умолчанию 3) — на каждый инструмент; превышение даёт `ErrMaxSteps`.
- `ToolTimeout` (по умолчанию 30 s) — таймаут вызова.
- Результат проходит guard на рельсе `tool_result`. При reject результат заменяется на `[blocked by guard]`.

## Дальше

| Цель                                             | Куда идти                             |
| ------------------------------------------------ | ------------------------------------- |
| Понять цикл вызова инструментов моделью          | [Chat](./chat.md)                     |
| Ограничить права роли на инструмент              | [Доступ](./access-control.md)         |
| Прочитать, как проверяется результат инструмента | [Guardrails](./guardrails.md)         |
| Посмотреть все поля `Config` и `ToolOption`      | [Справочник kit](../reference/kit.md) |

## Смотрите также

- [Справочник kit](../reference/kit.md) — `kit.Tool`, `Desc`, `Perm`
- [Бизнес-правила](../internals/business-rules.md) — `BR-15`…`BR-23`
- [demo](../../demo/README.ru.md) — реальный инструмент погоды
