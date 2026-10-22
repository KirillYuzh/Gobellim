# Трассировка и наблюдаемость (tracing)

`trace` — подсистема записи вызовов с маскированным текстом по умолчанию. Каждая
операция записывается в трейс, который можно экспортировать через OpenTelemetry
провайдер.

## Архитектура

Трейс-провайдер настраивается через `gobellim.Config.TracerProvider`. Если не задан,
используется noop-провайдер (трассировка не экспортируется, но трейсы всё ещё
формируются в памяти).

## Настройка

```go
import (
    sdktrace "go.opentelemetry.io/otel/sdk/trace"
    "github.com/KirillYuzh/Gobellim/plugins/otel"
)

tp, _ := sdktrace.NewTracerProvider(...) // your exporter, sampler, batching
app, err := gobellim.New(ctx, gobellim.Config{
    // ...
    TracerProvider: otel.NewProvider(tp.Tracer("gobellim")),
})
```

`trace` остаётся без внешних зависимостей: `plugins/otel` — единственный модуль,
который обращается к OpenTelemetry. Без `TracerProvider` операции всё равно
выполняются, просто их спаны никуда не пишутся.

## Данные в трейсе

Для каждой операции записывается:

- tenant, user_id, trace_id
- guard версия, модель, хеш политики
- политику текста (masked/full)
- вход и выход (если `TraceContent == ContentFull` — исходный текст, иначе маскированный; при `ContentNone` — только метаданные)
- токены и стоимость
- ошибки и статус

## Аудит-лог

Отдельный от трейса, записывает одну структурированную запись на операцию:
principal, tenant, операция, исход, тип ошибки, trace ID. Настраивается через
`Config.AuditLog` (отдельный `*slog.Logger`).

## Смотрите также

- [Быстрый старт](../overview.md)
- [Защита](../guard/README.md) — вердикты guard попадают в трейсы
- [Справочник API](../reference/gobellim.md) — `Config.TracerProvider`, `Config.AuditLog`, `Config.TraceContent`
