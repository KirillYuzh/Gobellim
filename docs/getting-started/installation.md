# Установка

kit подключается как обычный Go-модуль. Сборка по умолчанию не требует нативных
библиотек; ONNX Runtime нужен только для локального guard L2 и локального эмбеддера.

Экземпляр Elasticsearch и клиент LLM kit не поставляет — вы передаёте их сами.

## Когда использовать

- Проект на Go 1.24+ и нужны RAG, tool calling и guardrails без Python в проде.
- Сервис должен собираться и запускаться без нативных библиотек — тогда достаточно
  сборки по умолчанию.
- Нужен локальный guard L2 (gliner) или локальный эмбеддер в ONNX — тогда нужна
  ORT-сборка.

## Быстрый старт

Подключите модуль:

```sh
go get github.com/gobellim/kit
```

Проверьте сборку, `vet` и тесты:

```sh
go build ./... && go vet ./... && go test ./...
```

## Как использовать

### Требования

- Go 1.24 или новее (`go.mod`: `go 1.24`).
- `go build ./...` не требует нативных библиотек: код ONNX/ORT закрыт тегом сборки
  `//go:build ort`.
- Сборка с тегом `ort` включает cgo: `CGO_ENABLED=1`.
- Elasticsearch 8.x (basic-лицензия). Клиент Elasticsearch в зависимости kit не
  входит (D10): вы даёте его через интерфейс `store.ES` — единственный метод `Do`.
- Тестовые зависимости — только stdlib.

### Зависимости

Прямых зависимостей в сборке по умолчанию три.

| Модуль                                     | Для чего                                         |
| ------------------------------------------ | ------------------------------------------------ |
| `gopkg.in/yaml.v3`                         | `policy.yaml`, строгий режим `KnownFields(true)` |
| `github.com/invopop/jsonschema`            | JSON Schema из Go-структур инструментов          |
| `github.com/santhosh-tekuri/jsonschema/v6` | валидация аргументов и structured output         |

Зависимости ONNX Runtime подключаются только в сборке с тегом `ort`.

| Модуль                            | Для чего                          | Что нужно на машине             |
| --------------------------------- | --------------------------------- | ------------------------------- |
| `github.com/yalue/onnxruntime_go` | вызов ONNX Runtime                | `libonnxruntime.so` при запуске |
| `github.com/daulet/tokenizers`    | токенайзеры HF (`tokenizer.json`) | `libtokenizers.a` при сборке    |

### Сборка с ONNX Runtime

ORT-сборка нужна только для локального guard L2 и локального эмбеддера. Под тегом
`ort` guard L2 — реальный ONNX-детектор (`guard/gliner`), без Python в рантайме.

```sh
CGO_ENABLED=1 CGO_LDFLAGS="-L.ort/lib" go build -tags ort ./...
```

- `CGO_ENABLED=1` обязателен: `onnxruntime_go` и `tokenizers` используют cgo.
- При сборке нужен `libtokenizers.a`; `CGO_LDFLAGS` указывает каталог с ней
  (например, `.ort/lib`).
- При запуске нужна `libonnxruntime.dylib`/`.so`; путь к библиотеке задаёт
  `KIT_ORT_LIB` (по умолчанию `.ort/lib/libonnxruntime.dylib` или `.so`).

Модели загружаются заранее командой `kitctl model pull` (`cmd/kitctl`).
Guard-модель воспроизводится скриптом экспорта — [`tools/gliner`](../../tools/gliner/README.md).

> [!NOTE]
> Без тега `ort` слой guard L2 — заглушка, а `embed.NewLocal` возвращает ошибку.
> Сборка по умолчанию годится для чата и работы через удалённые провайдеры.

### Каталог моделей

Модель лежит в `<models_dir>/<org>__<name>/` и содержит три файла: `model.onnx`,
`tokenizer.json`, `config.json`.

`models_dir` берётся из опции (`embed.Dir`, `gliner.Config.Dir`), переменной
`KIT_MODELS_DIR` или по умолчанию — `./models`. Например,
`intfloat/multilingual-e5-small` ожидается в
`./models/intfloat__multilingual-e5-small/`, guard L2 — в
`./models/hivetrace__gliner-guard-omni/`.

`config.json` guard-модели — контракт для детектора: метки, пороги, `prefix_ids`,
позиции маркеров и формы тензоров ONNX. Смена схемы требует повторного экспорта
([`tools/gliner`](../../tools/gliner/README.md)).

### Локальный стек

Elasticsearch и Grafana поднимаются из `deploy/`:

```sh
cd deploy && docker compose up -d elasticsearch grafana
```

Elasticsearch слушает `9200`, Grafana — `3000`. Дашборды провижнятся из файлов.

## Дальше

| Цель                      | Куда идти                                   |
| ------------------------- | ------------------------------------------- |
| Собрать первое приложение | [Быстрый старт](./quickstart.md)            |
| Разобрать раскладку кода  | [Структура проекта](./project-structure.md) |
| Подключить свою модель    | [Провайдеры](../reference/providers.md)     |
| Настроить `policy.yaml`   | [Политика](../reference/policy.md)          |

## Смотрите также

- [Документация kit](../README.md) — индекс разделов
- [Устройство: архитектура](../internals/architecture.md) — пакеты, рантайм, безопасность
- [demo](../../demo/README.ru.md) — запуск сквозного примера
