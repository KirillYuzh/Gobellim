# Elasticsearch

`plugins/elasticsearch` это готовый транспорт `store.ES` поверх `net/http`.
Ядро строит запросы и разбирает ответы. Плагин отправляет их в кластер.

## Границы

Разделение обязанностей:

- `store` в ядре владеет именами индексов, маппингами, телами запросов и
  разбором ответов;
- `plugins/elasticsearch` владеет `net/http`, адресом, авторизацией и лимитом
  ответа.

Интерфейс один:

```go
type ES interface {
	Do(ctx context.Context, req Request) (statusCode int, body []byte, err error)
}
```

`Request` это явная структура. `Path` может уже содержать query string.
`Refresh` добавляет `?refresh=<v>` или `&refresh=<v>` в `EffectivePath`.
Тип контента и refresh применяет клиент.

## Конфигурация

```go
type Config struct {
	URL        string
	Username   string
	Password   string
	APIKey     string
	HTTPClient *http.Client
}
```

| Поле         | Обязательное | Смысл                                      |
| ------------ | ------------ | ------------------------------------------ |
| `URL`        | да           | абсолютный адрес, может нести префикс пути |
| `Username`   | нет          | basic auth вместе с `Password`             |
| `Password`   | нет          | basic auth вместе с `Username`             |
| `APIKey`     | нет          | заголовок `Authorization: ApiKey ...`      |
| `HTTPClient` | нет          | свой клиент; иначе таймаут 30 с            |

`New` возвращает `ErrInvalidConfig` для пустого или относительного URL, а также
когда заданы и `APIKey`, и `Username`/`Password`.

```go
if cfg.APIKey != "" && (cfg.Username != "" || cfg.Password != "") {
	return nil, fmt.Errorf("elasticsearch: APIKey and Username/Password are mutually exclusive: %w", core.ErrInvalidConfig)
}
```

## URL и префикс пути

Адрес должен содержать схему и хост. Хвостовой слэш у пути срезается. Префикс
из `URL` складывается с путём запроса, поэтому клиент подходит для кластера за
прокси:

```text
https://host/es  +  /kit-chunks/_search  ->  https://host/es/kit-chunks/_search
```

Сегменты `.` и `..` в пути запроса запрещены. `url.Parse` раскрывает `%2e%2e`,
поэтому такой путь тоже отвергается. Это защищает от выхода за префикс.

## Ответ и ошибки транспорта

`Do` возвращает статус и тело. Не-2xx статус не считается ошибкой транспорта:
`store` сам решает, что делать с ответом. Ошибкой считаются сбой сети, сбой
чтения и тело больше 32 MiB.

```go
// maxResponseBytes caps one response body read.
const maxResponseBytes = 32 << 20
```

Запрос без `Method` отвергается. `Content-Type` по умолчанию
`application/json`. Для `_bulk` и `_msearch` ядро передаёт
`application/x-ndjson` через `NDJSONRequest`.

Авторизация выбирается в таком порядке: `ApiKey`, затем basic auth. Оба
заголовка одновременно не ставятся.

## Индексы

| Имя                   | Тип         | Содержимое                               |
| --------------------- | ----------- | ---------------------------------------- |
| `kit-chunks`          | index       | чанки: текст, вектор, ACL                |
| `kit-memory-messages` | index       | сообщения тредов для recall              |
| `kit-memory-working`  | index       | рабочая память, один документ на тред    |
| `kit-traces`          | data stream | trace и score                            |
| `kit-observations`    | data stream | spans, generations, events               |
| `kit-traces-30d`      | ILM policy  | rollover при 10 GB, delete через 30 дней |

Настройки индексов: один шард, ноль реплик. Текстовые поля используют
анализатор `russian`. Смена анализатора требует переиндексации: `EnsureSchema`
существующий индекс не пересоздаёт.

## Маппинг `kit-chunks`

```go
VectorField: map[string]any{"type": "dense_vector", "dims": dims, "index": true, "similarity": "cosine"},
"position":  map[string]any{"type": "integer"},
"source":    map[string]any{"type": "keyword"},
"created_at": map[string]any{"type": "date"},
```

Поле `text` создаётся как `{"type": "text", "analyzer": "russian"}`. Поле `acl`
имеет тип `keyword` и хранит роли. Поле `tenant` тоже `keyword`. Поле
`embed_model` позволяет отфильтровать чанки, посчитанные другой моделью. Поле
`quarantined` помечает чанк, отклонённый guard. Такой чанк хранится без текста
и без вектора.

`ChunksIndexBody` требует `dims > 0`, иначе возвращает `ErrInvalidConfig`.
`MemoryMessagesIndexBody` добавляет вектор только при `dims > 0`.

## Запись и bulk

`store.BulkBody(index, docs)` собирает NDJSON с действием `index`.
`store.BulkCreateBody` использует `create`. `ChunksBulkBody` сначала проверяет
каждый чанк:

- `ID`, `DocID`, `Tenant` и `ACL` не пусты;
- карантинный чанк не несёт текст и вектор;
- обычный чанк несёт вектор нужной длины.

`BulkErrors` разбирает ответ. Пустое тело считается успехом. Ответ с `errors:
false` тоже успех. Ошибка элемента 409 или `version_conflict_engine_exception`
возвращается как `*VersionConflictError`. Остальные ошибки оборачивают
`store.ErrBulkItem`.

## Создание схемы

`store.EnsureSchema(ctx, es, dims)` выполняет шаги по порядку. Каждый шаг
ограничен 30 секундами.

| Шаг | Запрос                                   |
| --- | ---------------------------------------- |
| 1   | `PUT /_ilm/policy/kit-traces-30d`        |
| 2   | `PUT /_index_template/kit-traces`        |
| 3   | `PUT /_index_template/kit-observations`  |
| 4   | `PUT /kit-memory-messages`               |
| 5   | `PUT /kit-memory-working`                |
| 6   | `PUT /kit-chunks`, только при `dims > 0` |

Ответ с `resource_already_exists_exception` или `already_exists_exception`
считается успехом. Остальные не-2xx оборачивают `ErrProvider`. Ошибка шага
останавливает создание схемы.

## Обслуживание

| Функция                  | Запрос             | Замечание                            |
| ------------------------ | ------------------ | ------------------------------------ |
| `DeleteByDocBody`        | `_delete_by_query` | нужны `doc_id` и `tenant`            |
| `DeleteByDocVersionBody` | `_delete_by_query` | сохраняет чанки версии `keepVersion` |
| `PruneBody`              | `_delete_by_query` | удаляет по `@timestamp` старше срока |
| `ParseDeleteByQuery`     | разбор ответа      | падает на failures и конфликтах      |

`Snippet` отдаёт не больше 200 байт тела ошибки. Срез идёт по границе руны,
поэтому текст остаётся валидным UTF-8.

## Сборка и запуск

Плагин это отдельный модуль с `replace` на корень. Он собирается вместе с
остальными:

```sh
go -C plugins/elasticsearch build ./...
go -C plugins/elasticsearch test ./...
make check
```

Внешних зависимостей у модуля нет. Нужен только стандартный `net/http`.

## Отказы

| Симптом                     | Причина                      | Ошибка             |
| --------------------------- | ---------------------------- | ------------------ |
| `URL is required`           | пустой `Config.URL`          | `ErrInvalidConfig` |
| `must be absolute`          | нет схемы или хоста          | `ErrInvalidConfig` |
| `mutually exclusive`        | заданы API-ключ и basic auth | `ErrInvalidConfig` |
| `path ... is not allowed`   | сегмент `.` или `..`         | обычная ошибка     |
| `response exceeds N bytes`  | тело больше 32 MiB           | обычная ошибка     |
| `bootstrap ... status N`    | схема не создана             | `ErrProvider`      |
| `bulk ... version conflict` | конфликт версии при записи   | `ErrBulkItem`      |

## Тесты

`plugins/elasticsearch/elasticsearch_test.go` проверяет URL, авторизацию,
префикс пути, лимит ответа и запрет dot-сегментов. Тесты `store` проверяют
маппинги, bulk, ensure и обслуживание.

## Смотрите также

- [Плагины](./README.md), границы модулей
- [Справочник: Elasticsearch](../reference/elasticsearch.md), полные маппинги
- [Гибридный поиск](./hybrid-search.md), `_msearch` и RRF
- [Поиск и индексация](../retrieval/README.md), `Ingest`, `Search`, `Delete`
