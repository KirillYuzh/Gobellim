# Поиск

Ingest превращает документ в чанки, считает ACL и пишет их в Elasticsearch. Search ищет по чанкам гибридно — kNN и BM25 — и возвращает только то, что разрешено принципалу.

## Когда использовать

- Ответ нужен по вашим документам, а не по данным модели.
- Доступ к документам различается по ролям и тенанту.
- Нужен контроль над разбиением текста на чанки.

## Быстрый старт

Файл: `demo/internal/adapter/kitengine/engine.go`

```go
ctx = kit.WithPrincipal(ctx, kit.Principal{
	UserID: "u-1",
	Roles:  []string{"support"},
	Tenant: "acme",
})

if err := app.Ingest(ctx, kit.Doc{
	ID:     "faq-1",
	Text:   "Возврат оформляется в течение 14 дней.",
	ACL:    []string{"support"},
	Source: "faq",
}); err != nil {
	return err
}

hits, err := app.Search(ctx, kit.SearchRequest{Query: "возврат", TopK: 5})
```

## Как использовать

### Ingest

`func (a *App) Ingest(ctx context.Context, doc Doc) error` требует право `ingest`.

1. Чанкование по токенам с overlap. По умолчанию `ChunkSize` — 400 токенов, `ChunkOverlap` — 80.
2. ACL считается при записи: tenant из принципала и роли из `Doc.ACL` пишутся в каждый чанк.
3. Каждый чанк проходит guard. Отклонённый чанк уходит в карантин (`quarantined: true`) и не индексируется.
4. Чанки пишутся в индекс `kit-chunks` через `_bulk?refresh=wait_for`: запись видна поиску сразу после возврата `Ingest`.

`Doc` требует непустой `ID`, иначе `ErrInvalidConfig`. Для `Ingest` и `Search` в `Config` нужен `Embedder`.

### Search

`func (a *App) Search(ctx context.Context, req SearchRequest) ([]SearchHit, error)` требует право `search`.

1. Запрос эмбеддится через `Query()` с обязательным префиксом.
2. Строятся два запроса — kNN и BM25, каждый с ACL-фильтром принципала (tenant + roles).
3. Оба выполняются одним `_msearch`.
4. Слияние RRF считается в Go; возвращается top-k (`TopK`, по умолчанию 10).

Пустой `Query` даёт `ErrInvalidArgs`.

### Deny-by-default

Чанк без совпадения ACL не возвращается. Пустой список ролей даёт `terms: {"acl": []}`, который не совпадает ни с чем.

### Типы

```go
type Doc struct {
	ID     string
	Text   string
	ACL    []string
	Source string
}

type SearchRequest struct {
	Query string
	TopK  int
}

type SearchHit struct {
	ID       string  `json:"-"`
	DocID    string  `json:"doc_id"`
	Text     string  `json:"text"`
	Position int     `json:"position"`
	Score    float64 `json:"-"`
}
```

### Delete

`func (a *App) Delete(ctx context.Context, docID string) error` требует право `delete` и удаляет по `doc_id` плюс tenant принципала (`_delete_by_query`).

### Эмбеддер

API эмбеддера — `Query()` и `Passages()`, без общего `Embed()`. `Query` и `Passages` разделены, чтобы префиксы нельзя было забыть. Ingest использует `Passages()`, Search — `Query()`.

- e5 (`intfloat/multilingual-e5-*`): mean pooling, префиксы `query: ` / `passage: `, L2-норма.
- bge-m3 (`BAAI/bge-m3`): CLS pooling, без префиксов, L2-норма.

Неизвестную модель описывают файлом `kit_embedder.json` рядом с весами, иначе `NewLocal` возвращает ошибку.

## Дальше

| Цель                                        | Куда идти                                      |
| ------------------------------------------- | ---------------------------------------------- |
| Ограничить, что принципал видит в поиске    | [Доступ](./access-control.md)                  |
| Прочитать про карантин при ingest           | [Guardrails](./guardrails.md)                  |
| Схема индекса и запросы `_msearch`          | [Elasticsearch](../reference/elasticsearch.md) |
| Подключить локальный или удалённый эмбеддер | [Провайдеры](../reference/providers.md)        |

## Смотрите также

- [Справочник kit](../reference/kit.md) — `Doc`, `SearchRequest`, `SearchHit`
- [Бизнес-правила](../internals/business-rules.md) — `BR-10`…`BR-14`, `BR-34`…`BR-39`
- [Устройство](../internals/architecture.md) — рантайм ORT и эмбеддер
