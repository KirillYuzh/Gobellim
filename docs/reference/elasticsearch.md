# Elasticsearch

Elasticsearch — единственное хранилище kit: он держит чанки документов и трейсы вызовов. Клиент пишет пользователь; kit формирует `store.Request` и разбирает ответы.

> [!NOTE]
> Требования: Elasticsearch ≥ 8.14 на basic-лицензии. Нужны data streams и `dense_vector`; гибридное слияние считается в Go, поэтому платный retriever не требуется.

## store.Request — контракт запроса

ES подключается через интерфейс `store.ES` (I-12, D21). Запрос — явная структура,
а не строка пути:

```go
type Request struct {
    Method      string // GET, POST, PUT, DELETE, HEAD
    Path        string // может уже нести query string
    Body        []byte
    ContentType string // "" -> application/json
    Refresh     string // "", "true", "wait_for" -> ?refresh=<v>
    Idempotent  bool   // можно ретраить без последствий
}

func (r Request) EffectivePath() string

type ES interface {
    Do(ctx context.Context, req Request) (statusCode int, body []byte, err error)
}

func JSONRequest(method, path string, body any) (Request, error)
func NDJSONRequest(method, path string, body []byte) Request
func RawRequest(method, path string, body []byte, contentType string) Request
```

**Тип контента и refresh применяет клиент вызывающего, а не kit.** `EffectivePath`
добавляет `?refresh=<v>` (или `&`, если в пути уже есть `?`); клиент обязан
отправить запрос именно по этому пути и подставить `Content-Type`
(`application/json`, если поле пустое). Пример минимального клиента —
[demo/internal/adapter/elastic](../../demo/internal/adapter/elastic/es.go).

## Индексы и потоки

| Имя                | Тип         | Содержимое                               |
| ------------------ | ----------- | ---------------------------------------- |
| `kit-chunks`       | index       | чанки документов: текст, вектор, ACL     |
| `kit-traces`       | data stream | по одному документу на trace и на score  |
| `kit-observations` | data stream | spans, generations, events               |
| `kit-traces-30d`   | ILM policy  | rollover при 10 GB, delete через 30 дней |

## kit-chunks

| Поле          | Тип                                              | Назначение                                        |
| ------------- | ------------------------------------------------ | ------------------------------------------------- |
| `doc_id`      | `keyword`                                        | Исходный документ.                                |
| `tenant`      | `keyword`                                        | Тенант из принципала.                             |
| `acl`         | `keyword`                                        | Роли, которым разрешён чанк.                      |
| `text`        | `text`                                           | Текст чанка (BM25).                               |
| `vector`      | `dense_vector` `dims=<Dim>`, `similarity=cosine` | Эмбеддинг; размерность берётся из эмбеддера.      |
| `position`    | `integer`                                        | Порядковый номер чанка в документе.               |
| `source`      | `keyword`                                        | Источник документа.                               |
| `created_at`  | `date`                                           | Время записи.                                     |
| `quarantined` | `boolean`                                        | Чанк, отклонённый guard; никогда не возвращается. |

Настройки индекса: `number_of_shards` = 1, `number_of_replicas` = 0. Карантинный
чанк хранится маркером `quarantined: true` **без** `text` и без `vector`
(BR-31, D19), поэтому его нельзя ни найти, ни использовать.

## kit-traces

| Поле                      | Тип                         | Назначение                              |
| ------------------------- | --------------------------- | --------------------------------------- |
| `@timestamp`              | `date`                      | Время окончания trace.                  |
| `trace_id`                | `keyword`                   | Идентификатор trace.                    |
| `name`                    | `keyword`                   | `chat`, `ingest`, `search`, `generate`. |
| `user_id`                 | `keyword`                   | Принципал.                              |
| `session_id`              | `keyword`                   | Группировка по сессии.                  |
| `tenant`                  | `keyword`                   | Тенант принципала.                      |
| `input`                   | `text`                      | Вход (под политикой текста; см. ниже).  |
| `output`                  | `text`                      | Выход.                                  |
| `tags`                    | `keyword`                   | Метки.                                  |
| `kind`                    | `keyword`                   | `SCORE` на документе оценки.            |
| `score`                   | `double`                    | Значение оценки (документ score).       |
| `comment`                 | `text`                      | Комментарий оценки (документ score).    |
| `guard_version`           | `keyword`                   | Аудит: версия guard (I-08).             |
| `guard_model`             | `keyword`                   | Аудит: id модели guard.                 |
| `policy_hash`             | `keyword`                   | Аудит: хэш политики.                    |
| `text_policy`             | `keyword`                   | Аудит: `masked` или `full`.             |
| `metadata`                | `object` (`enabled: false`) | Произвольные данные, не индексируются.  |
| `usage.prompt_tokens`     | `integer`                   | Токены промпта.                         |
| `usage.completion_tokens` | `integer`                   | Токены ответа.                          |
| `usage.total_tokens`      | `integer`                   | Всего токенов.                          |
| `usage.cost`              | `double`                    | Стоимость.                              |

Score пишется сюда же: `kind: SCORE`, `trace_id`, `name`, `score`, `comment`.

## kit-observations

| Поле                | Тип                         | Назначение                                                                                                                                                                                      |
| ------------------- | --------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `@timestamp`        | `date`                      | Время окончания observation.                                                                                                                                                                    |
| `trace_id`          | `keyword`                   | Ссылка на trace.                                                                                                                                                                                |
| `observation_id`    | `keyword`                   | Идентификатор observation.                                                                                                                                                                      |
| `parent_id`         | `keyword`                   | Родительская observation.                                                                                                                                                                       |
| `name`              | `keyword`                   | Имя операции (`tool`, `guard:<rail>`, модель).                                                                                                                                                  |
| `kind`              | `keyword`                   | `SPAN`, `GENERATION`, `EVENT`.                                                                                                                                                                  |
| `level`             | `keyword`                   | `DEFAULT`, `ERROR`.                                                                                                                                                                             |
| `error_kind`        | `keyword`                   | `unauthorized`, `guard_reject`, `guard_unavailable`, `invalid_args`, `tool_not_found`, `tool`, `max_steps`, `budget`, `rate_limited`, `approval_required`, `not_found`, `provider`, `internal`. |
| `model`             | `keyword`                   | Модель вызова.                                                                                                                                                                                  |
| `guard_rail`        | `keyword`                   | Rail guard при срабатывании.                                                                                                                                                                    |
| `guard_action`      | `keyword`                   | `allow`, `detect`, `mask`, `reject`.                                                                                                                                                            |
| `guard_categories`  | `keyword`                   | Категории находок (`PII`, `SECRET`, `INJECTION`, `UNSAFE`).                                                                                                                                     |
| `guard_version`     | `keyword`                   | Аудит: версия guard (I-08).                                                                                                                                                                     |
| `guard_model`       | `keyword`                   | Аудит: id модели guard.                                                                                                                                                                         |
| `policy_hash`       | `keyword`                   | Аудит: хэш политики.                                                                                                                                                                            |
| `text_policy`       | `keyword`                   | Аудит: `masked` или `full`.                                                                                                                                                                     |
| `tags`              | `keyword`                   | Метки.                                                                                                                                                                                          |
| `input`             | `text`                      | Вход вызова.                                                                                                                                                                                    |
| `output`            | `text`                      | Выход вызова.                                                                                                                                                                                   |
| `status_message`    | `text`                      | Текст ошибки.                                                                                                                                                                                   |
| `start_time`        | `date`                      | Начало.                                                                                                                                                                                         |
| `end_time`          | `date`                      | Конец.                                                                                                                                                                                          |
| `latency_ms`        | `double`                    | Длительность в миллисекундах.                                                                                                                                                                   |
| `prompt_tokens`     | `integer`                   | Токены промпта.                                                                                                                                                                                 |
| `completion_tokens` | `integer`                   | Токены ответа.                                                                                                                                                                                  |
| `total_tokens`      | `integer`                   | Всего токенов.                                                                                                                                                                                  |
| `cost`              | `double`                    | Стоимость.                                                                                                                                                                                      |
| `metadata`          | `object` (`enabled: false`) | Не индексируется.                                                                                                                                                                               |

Пустые поля (`error_kind`, `guard_*`, аудит-поля) в документ не попадают.

## Что создаёт App.Setup

`App.Setup` вызывает `store.EnsureSchema` и создаёт:

| Шаг | Запрос                                                      |
| --- | ----------------------------------------------------------- |
| 1   | `PUT /_ilm/policy/kit-traces-30d`                           |
| 2   | `PUT /_index_template/kit-traces`                           |
| 3   | `PUT /_index_template/kit-observations`                     |
| 4   | `PUT /kit-chunks` — только если размерность эмбеддера `> 0` |

Шаблоны data stream задают 1 shard, 0 replicas и lifecycle `kit-traces-30d`. Вызов идемпотентен: ответы `resource_already_exists_exception` и `already_exists_exception` считаются успехом. Если `Embedder` не задан, `Dim()` не даёт размерности и шаг 4 пропускается. Каждый шаг ограничен `store.EnsureStepTimeout` (по умолчанию 30 с).

## Поиск

`App.Search` вызывает `_msearch` с двумя подзапросами — kNN и BM25, — каждый со своим ACL-фильтром. `_source` ограничен полями `doc_id`, `text`, `position`. `k` задаётся top-level `size`, в самом `knn`-запросе поля `k` нет; `num_candidates` по умолчанию — максимум из `topK * 10` и 100.

```json
{"index":"kit-chunks"}
{"size":10,"query":{"knn":{"field":"vector","query_vector":[],"num_candidates":100,
  "filter":{"bool":{"filter":[{"term":{"tenant":"t1"}},{"terms":{"acl":["support"]}},{"term":{"quarantined":false}}]}}}},"_source":["doc_id","text","position"]}
{"index":"kit-chunks"}
{"size":10,"query":{"bool":{"must":[{"match":{"text":"вопрос"}}],
  "filter":[{"bool":{"filter":[{"term":{"tenant":"t1"}},{"terms":{"acl":["support"]}},{"term":{"quarantined":false}}]}}]}},"_source":["doc_id","text","position"]}
```

ACL-клауза: совпадение `tenant` с тенантом принципала, вхождение одной из ролей принципала в `acl` и `quarantined: false`. Пустой список ролей даёт `{"terms":{"acl":[]}}`, что не совпадает ни с чем (deny-by-default). Непустой `tenant` обязателен: без него `ACLFilter` возвращает ошибку. `TopK` клампится до `1000` (`store.MaxTopK`), `num_candidates` — до `10000` (`store.MaxNumCandidates`).

Ошибка любой ветви `_msearch` (`error` или `_shards.failed > 0`) делает ошибкой весь вызов, а не молча отбрасывает ветвь.

Ответы сливаются алгоритмом RRF в Go: `score(id) = Σ_lists 1 / (rankConstant + rank + 1)`, `rankConstant` = 60 (`store.DefaultRRFRankConst`), ничьи разбиваются по возрастанию `_id`. Возвращается top-k.

### Фильтр метаданных

`SearchRequest.Filter` (`store.Filter`) — маленький язык операторов, компилируемый
в ES-query и AND-ищийся с ACL-клаузой:

| Операторы                                                                        | Разрешённые поля                                        |
| -------------------------------------------------------------------------------- | ------------------------------------------------------- |
| `$eq` `$ne` `$gt` `$gte` `$lt` `$lte` `$in` `$nin` `$exists` `$and` `$or` `$not` | `doc_id` `source` `position` `created_at` `quarantined` |

Голое значение трактуется как `$eq`. Неизвестный оператор или поле — ошибка с
`ErrInvalidArgs`.

```go
store.Filter{"doc_id": "1", "position": map[string]any{"$gte": 2}}
```

## Запись

| Что                          | Запрос                                                                                     |
| ---------------------------- | ------------------------------------------------------------------------------------------ |
| чанки                        | `POST /_bulk?refresh=wait_for` с действием `index`                                         |
| трейсы, observations, scores | `POST /_bulk` с действием `create` — data streams append-only                              |
| удаление документа           | `POST /kit-chunks/_delete_by_query?refresh=true` с фильтром `doc_id` + `tenant` принципала |
| повторный ingest             | тот же `_delete_by_query` перед записью новых чанков, чтобы не оставить хвосты             |

Запись трейсов асинхронная: при переполнении очереди документы отбрасываются и не блокируют запрос. Ответ `_bulk` разбирается по итемам: `409`/`version_conflict_engine_exception` считается успехом (документ уже есть), любая другая ошибка итема — ошибка.

## Ретенция и prune

`App.Prune(ctx, olderThan)` удаляет старые трейсы и observations:

| Поток              | Запрос                                                              |
| ------------------ | ------------------------------------------------------------------- |
| `kit-traces`       | `POST /kit-traces/_delete_by_query` по `range @timestamp lt` cutoff |
| `kit-observations` | `POST /kit-observations/_delete_by_query` по тому же диапазону      |

`cutoff = now - olderThan` (`store.PruneBody`); `req.Refresh = "false"`. Ответ
разбирается через `store.ParseDeleteByQuery`: `failures` и `version_conflicts`
считаются ошибкой, даже если ES ответил `200`. Плюс есть ILM-политика
`kit-traces-30d` (rollover при 10 GB, delete через 30 дней) — prune дополняет,
а не заменяет её.

## Смотрите также

- [Справочник kit](./kit.md) — `App.Setup`, `Search`, `Ingest`, `Delete`, `Prune`
- [Поиск](../concepts/retrieval.md) — гибридный поиск и слияние RRF
- [Наблюдаемость](../concepts/observability.md) — трейсы и оценки
- [Конфигурация](./configuration.md) — лимиты и значения по умолчанию трейсинга
