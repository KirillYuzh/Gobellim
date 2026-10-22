# Установка

## Требования

- Go 1.26 или выше
- Elasticsearch (опционально, нужен для `Ingest`/`Search`/`Delete`/`Prune` и памяти)
- LLM-провайдер (OpenAI-совместимый, Ollama, vLLM, GigaChat)

## Установка

```sh
go get github.com/KirillYuzh/Gobellim
```

## Сборка

```sh
# Обычная сборка (без нативных библиотек, без ONNX)
go build ./...

# Сборка с ONNX (для L2-детектора guard/gliner)
go build -tags ort ./...

# Все проверки (build, vet, тесты, lint, fumpt)
make check
```

## Переменные окружения

| Переменная             | Описание                           |
| ---------------------- | ---------------------------------- |
| `KIT_MODELS_DIR`       | директория с моделями              |
| `KIT_ORT_LIB`          | путь к ONNX Runtime библиотеке     |
| `GIGACHAT_CREDENTIALS` | учётные данные GigaChat (для демо) |

## Проверка окружения

```sh
go test ./authz/... ./chunk/... ./embed/... ./guard/... ./store/... ./internal/tool/... ./internal/importrules/... ./internal/call/... ./tool/... -count=1
go vet ./...
```

## Следующие шаги

После установки см. [Быстрый старт](./overview.md) и тематические разделы:
[Инструменты](./tools/README.md), [Защита](./guard/README.md), [Трассировка](./tracing/README.md).
