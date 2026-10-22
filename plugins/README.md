# Plugins

Optional integrations live here as **separate Go modules**, one per vendor. The
core module never imports them: `go build ./...` at the repository root stays
free of vendor code, and `internal/importrules` rejects a `plugins/...` import
from the core (`core-no-optional`).

| Module                       | Supplies                                                        |
| ---------------------------- | --------------------------------------------------------------- |
| `plugins/elasticsearch`      | a concrete `store.ES` client over `net/http`                     |
| `plugins/gigachat`           | `model.Client` and `embed.Embedder` backed by the GigaChat API   |

## Using a plugin

Add the module and pass the client to `gobellim.Config`:

```go
es, err := elasticsearch.New(elasticsearch.Config{
    URL:    "https://es.internal:9200",
    APIKey: os.Getenv("ES_API_KEY"),
})
app, err := gobellim.New(ctx, gobellim.Config{
    ES:  es,
    LLM: gigachatClient,
})
```

## Adding a plugin

1. Create `plugins/<vendor>/go.mod`:

   ```go
   module github.com/KirillYuzh/Gobellim/plugins/<vendor>

   go 1.26.0

   require github.com/KirillYuzh/Gobellim v0.1.0

   replace github.com/KirillYuzh/Gobellim => ../..
   ```

2. Implement the core interface (`model.Client`, `embed.Embedder`, `store.ES`,
   …); import the core package, never the other way round.
3. Put the module path in `MODULES` (`Makefile`) and `OPTIONAL_MODULES`
   (`.github/workflows/ci.yml`), so `make check` and CI build, vet, test and
   lint it.
4. Document the constructor and its config in `docs/reference/providers.md`
   (or the matching reference page) in the same change.
