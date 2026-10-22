# Поиск и индексация (retrieval)

`Ingest` режет документ на чанки, считает векторы и пишет их в Elasticsearch.
`Search` выполняет BM25 и kNN с фильтром по ACL принципала, получает результаты через RRF и склеивает в итоговый список.

## Добавление данных

```go
err := app.Ingest(ctx, gobellim.Doc{
	ID:     "doc-1",
	Text:   "большой текст документа...",
	ACL:    []string{"user"},
	Source: "handbook",
})
```

## Поиск

```go
hits, err := app.Search(ctx, gobellim.SearchRequest{
	Query:  "политика возврата",
	TopK:   10,
	Rerank: true,
})
```

Запрос проходит через guard (input rail): `Reject` останавливает поиск, `Mask` —
то, что достигает эмбеддера, BM25 и трейс.

## ACL

`Doc.ACL` валидируется на непустоту до эмбеддинга. Пустой ACL индексирует
документ, который никто не может найти. Поиск применяет tenant, роли и
`quarantined: false` к обоим подзапросам `_msearch` (kNN и BM25).

## Смотрите также

- [Быстрый старт](../overview.md)
- [Защита](../guard/README.md) — query проходит через guard перед эмбеддером
- [Справочник API](../reference/gobellim.md) — `gobellim.Doc`, `gobellim.SearchRequest`, `gobellim.SearchHit`
