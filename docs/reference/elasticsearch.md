# Elasticsearch

Elasticsearch — единственное хранилище kit: он держит чанки документов и трейсы вызовов. Клиент пишет пользователь (интерфейс `store.ES`: `Do(ctx, method, path, body)`), kit формирует тела запросов и разбирает ответы.

> [!NOTE]
> Требования: Elasticsearch ≥ 8.14 на basic-лицензии. Нужны data streams и `dense_vector`; гибридное слияние считается в Go, поэтому платный retriever не требуется.

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

Настройки индекса: `number_of_shards` = 1, `number_of_replicas` = 0.

## kit-traces

| Поле                      | Тип                         | Назначение                                           |
| ------------------------- | --------------------------- | ---------------------------------------------------- |
| `@timestamp`              | `date`                      | Время окончания trace.                               |
| `trace_id`                | `keyword`                   | Идентификатор trace.                                 |
| `name`                    | `keyword`                   | `chat`, `ingest`, `search`, `generate`.              |
| `user_id`                 | `keyword`                   | Принципал.                                           |
| `session_id`              | `keyword`                   | Группировка по сессии.                               |
| `tenant`                  | `keyword`                   | Тенант принципала.                                   |
| `input`                   | `text`                      | Вход (маскированный, если `TraceStoreRaw` выключен). |
| `output`                  | `text`                      | Выход.                                               |
| `tags`                    | `keyword`                   | Метки.                                               |
| `metadata`                | `object` (`enabled: false`) | Произвольные данные, не индексируются.               |
| `usage.prompt_tokens`     | `integer`                   | Токены промпта.                                      |
| `usage.completion_tokens` | `integer`                   | Токены ответа.                                       |
| `usage.total_tokens`      | `integer`                   | Всего токенов.                                       |
| `usage.cost`              | `double`                    | Стоимость.                                           |

Score пишется сюда же: `kind: SCORE`, `trace_id`, `name`, `score`, `comment`.

## kit-observations

| Поле                | Тип                         | Назначение                                                                                                                               |
| ------------------- | --------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| `@timestamp`        | `date`                      | Время окончания observation.                                                                                                             |
| `trace_id`          | `keyword`                   | Ссылка на trace.                                                                                                                         |
| `observation_id`    | `keyword`                   | Идентификатор observation.                                                                                                               |
| `parent_id`         | `keyword`                   | Родительская observation.                                                                                                                |
| `name`              | `keyword`                   | Имя операции (`tool`, `guard:<rail>`, модель).                                                                                           |
| `kind`              | `keyword`                   | `SPAN`, `GENERATION`, `EVENT`.                                                                                                           |
| `level`             | `keyword`                   | `DEFAULT`, `ERROR`.                                                                                                                      |
| `error_kind`        | `keyword`                   | `unauthorized`, `guard_reject`, `guard_unavailable`, `invalid_args`, `tool_not_found`, `max_steps`, `not_found`, `provider`, `internal`. |
| `model`             | `keyword`                   | Модель вызова.                                                                                                                           |
| `guard_rail`        | `keyword`                   | Rail guard при срабатывании.                                                                                                             |
| `guard_action`      | `keyword`                   | `allow`, `mask`, `reject`.                                                                                                               |
| `guard_categories`  | `keyword`                   | Категории находок (`PII`, `SECRET`, `INJECTION`, `UNSAFE`).                                                                              |
| `input`             | `text`                      | Вход вызова.                                                                                                                             |
| `output`            | `text`                      | Выход вызова.                                                                                                                            |
| `status_message`    | `text`                      | Текст ошибки.                                                                                                                            |
| `start_time`        | `date`                      | Начало.                                                                                                                                  |
| `end_time`          | `date`                      | Конец.                                                                                                                                   |
| `latency_ms`        | `double`                    | Длительность в миллисекундах.                                                                                                            |
| `prompt_tokens`     | `integer`                   | Токены промпта.                                                                                                                          |
| `completion_tokens` | `integer`                   | Токены ответа.                                                                                                                           |
| `total_tokens`      | `integer`                   | Всего токенов.                                                                                                                           |
| `cost`              | `double`                    | Стоимость.                                                                                                                               |
| `metadata`          | `object` (`enabled: false`) | Не индексируется.                                                                                                                        |

Пустые поля (`error_kind`, `guard_*`) в документ не попадают.

## Что создаёт App.Setup

`App.Setup` вызывает `store.EnsureSchema` и создаёт:

| Шаг | Запрос                                                      |
| --- | ----------------------------------------------------------- |
| 1   | `PUT /_ilm/policy/kit-traces-30d`                           |
| 2   | `PUT /_index_template/kit-traces`                           |
| 3   | `PUT /_index_template/kit-observations`                     |
| 4   | `PUT /kit-chunks` — только если размерность эмбеддера `> 0` |

Шаблоны data stream задают 1 shard, 0 replicas и lifecycle `kit-traces-30d`. Вызов идемпотентен: ответы `resource_already_exists_exception` и `already_exists_exception` считаются успехом. Если `Embedder` не задан, `Dim()` не даёт размерности и шаг 4 пропускается.

## Поиск

`App.Search` вызывает `_msearch` с двумя подзапросами — kNN и BM25, — каждый со своим ACL-фильтром. `_source` ограничен полями `doc_id`, `text`, `position`.

```json
{"index":"kit-chunks"}
{"size":10,"query":{"knn":{"field":"vector","query_vector":[],"k":10,"num_candidates":100,
  "filter":{"bool":{"filter":[{"term":{"tenant":"t1"}},{"terms":{"acl":["support"]}},{"term":{"quarantined":false}}]}}}}}
{"index":"kit-chunks"}
{"size":10,"query":{"bool":{"must":[{"match":{"text":"вопрос"}}],
  "filter":[{"bool":{"filter":[{"term":{"tenant":"t1"}},{"terms":{"acl":["support"]}},{"term":{"quarantined":false}}]}}]}}}
```

`num_candidates` по умолчанию — максимум из `topK * 10` и 100. ACL-клауза: совпадение `tenant` с тенантом принципала, вхождение одной из ролей принципала в `acl` и `quarantined: false`. Пустой список ролей даёт `{"terms":{"acl":[]}}`, что не совпадает ни с чем (deny-by-default).

Ответы сливаются алгоритмом RRF в Go: `score(id) = Σ_lists 1 / (rankConstant + rank + 1)`, `rankConstant` = 60 (`store.DefaultRRFRankConst`). Возвращается top-k.

## Запись

| Что                          | Запрос                                                                                     |
| ---------------------------- | ------------------------------------------------------------------------------------------ |
| чанки                        | `POST /_bulk?refresh=wait_for` с действием `index`                                         |
| трейсы, observations, scores | `POST /_bulk` с действием `create` — data streams append-only                              |
| удаление документа           | `POST /kit-chunks/_delete_by_query?refresh=true` с фильтром `doc_id` + `tenant` принципала |

Запись трейсов асинхронная: при переполнении очереди документы отбрасываются и не блокируют запрос.

## Смотрите также

- [Справочник kit](./kit.md) — `App.Setup`, `Search`, `Ingest`, `Delete`
- [Поиск](../concepts/retrieval.md) — гибридный поиск и слияние RRF
- [Наблюдаемость](../concepts/observability.md) — трейсы и оценки
- [Конфигурация](./configuration.md) — лимиты и значения по умолчанию трейсинга
