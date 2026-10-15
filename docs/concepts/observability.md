# Наблюдаемость

kit пишет трейсы вызовов в Elasticsearch. Trace создаётся на входе операции, а
каждый внутренний вызов — модели, инструмента, эмбеддера, Elasticsearch, guard —
становится observation.

Модель данных по смыслу совпадает с Langfuse: trace → observations → scores.
Session — это группировка по `session_id`, отдельной сущности нет.

## Когда использовать

- Нужно видеть латентность, стоимость и ошибки по каждому запросу.
- Нужно знать, какие инструменты вызывает модель и где срабатывает guard.
- Нужно считать расход по модели и тенанту.

Трейсинг включён всегда: писатель создаётся в `kit.New`, отдельного флага
включения нет.

## Быстрый старт

`main.go`

```go
app, err := kit.New(ctx, kit.Config{ES: es, LLM: llm, Policy: policy})
if err != nil {
    return err
}
if err := app.Setup(ctx); err != nil { // создаёт data stream и ILM-политику
    return err
}

answer, err := app.Chat(ctx, kit.ChatRequest{
    System:   "Ты ассистент поддержки.",
    Messages: []model.Message{{Role: model.RoleUser, Content: "Какая погода?"}},
})
// trace и observations уже ушли в очередь асинхронного писателя

// Перед остановкой сервиса дождаться сброса очереди.
return app.Close(ctx)
```

`terminal`

```sh
curl -s 'http://localhost:9200/kit-traces/_search?size=1&pretty' \
  -H 'Content-Type: application/json' \
  -d '{"query":{"term":{"name":"chat"}}}'
```

## Как использовать

### Что создаётся

| Сущность    | Когда                                         | Где                |
| ----------- | --------------------------------------------- | ------------------ |
| trace       | вход в `Chat`, `Ingest`, `Search`, `Generate` | `kit-traces`       |
| observation | каждый вызов через `internal/call`            | `kit-observations` |
| score       | вызов `App.Score`                             | `kit-traces`       |

Trace несёт `name` (`chat`, `ingest`, `search`, `generate`), `user_id`, `tenant`,
`session_id`, вход, выход и `usage` с токенами и стоимостью.

Observation несёт `kind` (`SPAN`, `GENERATION`, `EVENT`), `name`, `level`,
`model`, `latency_ms`, токены и `cost`. Проверки guard пишутся как `EVENT` с
именем `guard:<rail>`; отказ добавляет `guard_rail`, `guard_action` и
`guard_categories`. Пустые поля в документ не попадают.

### Модель данных

| Уровень     | Что описывает                                                          |
| ----------- | ---------------------------------------------------------------------- |
| trace       | один запрос целиком, от входа до ответа                                |
| observation | шаг внутри запроса: вызов модели, инструмента, эмбеддера, ES или guard |
| score       | пользовательская оценка trace: имя, число, комментарий                 |

`session_id` группирует трейсы в сессию. Отдельного документа для сессии нет.

Поля и типы документов — [Индексы Elasticsearch](../reference/elasticsearch.md).

### Асинхронная запись

Писатель не блокирует запрос: документы уходят в буфер-канал и отправляются
батчами.

| Параметр               | Значение          |
| ---------------------- | ----------------- |
| очередь                | 4096 документов   |
| размер батча           | 500 документов    |
| интервал сброса        | 2 с               |
| лимит батча по размеру | 5 MB              |
| повторы при ошибке     | 3, backoff 200 мс |

При переполнении очереди новые документы отбрасываются: запрос не ждёт, растёт
счётчик `dropped`, о переполнении пишется `slog.Warn`. Стриминг трейсов в ES идёт
через `_bulk` с действием `create` — data streams append-only.

`App.Close(ctx)` закрывает очередь и дописывает остаток. Вызывайте его перед
остановкой сервиса, иначе последние документы не дойдут до Elasticsearch.

### Маскирование

В ES пишется замаскированный текст; реальные значения PII и секретов в трейсы не
попадают. Флаг `TraceStoreRaw` по умолчанию выключен — его включение пишет
необработанный текст и снимает эту защиту.

### Хранение

Traces и observations хранятся в data streams `kit-traces` и `kit-observations`.
ILM-политика `kit-traces-30d` делает rollover при 10 GB и удаляет документы через
30 дней.

### Дашборды

В `deploy/grafana/` лежат provisioning datasource и дашборд `kit-overview`
(`deploy/grafana/dashboards/kit-overview.json`). Панели читают data streams
напрямую:

| Панель                                    | Источник                                  |
| ----------------------------------------- | ----------------------------------------- |
| латентность LLM p95                       | `kit-observations`, `kind:GENERATION`     |
| ошибки по `kind` (guard, authz, provider) | `kit-observations`, `level:ERROR`         |
| токены по модели                          | `kit-observations`, `kind:GENERATION`     |
| срабатывания guard по rail                | `kit-observations`, `guard_action:reject` |
| топ инструментов                          | `kit-observations`, `kind:SPAN`           |
| стоимость по тенанту                      | `kit-traces`, `usage.cost`                |
| последние trace                           | `kit-traces`                              |

## Дальше

| Цель                           | Куда идти                                              |
| ------------------------------ | ------------------------------------------------------ |
| Ограничить вход и выход модели | [Guardrails](./guardrails.md)                          |
| Настроить права                | [Доступ](./access-control.md)                          |
| Описать поля документов        | [Индексы Elasticsearch](../reference/elasticsearch.md) |
| Задать режим записи текста     | [Конфигурация](../reference/configuration.md)          |

## Смотрите также

- [Chat](./chat.md) — где создаётся trace запроса
- [Справочник kit](../reference/kit.md) — `App.Score`, `App.Close`
- [Архитектура](../internals/architecture.md) — раздел Observability
- [Бизнес-правила](../internals/business-rules.md) — `BR-40`…`BR-47`
