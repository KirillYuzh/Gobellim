# Инструменты gliner

Экспорт и воспроизведение guard-модели L2 (`hivetrace/gliner-guard-omni`) в
ONNX. Здесь два файла:

| Файл        | Что                                                                   |
| ----------- | --------------------------------------------------------------------- |
| `export.py` | экспорт графа, генерация фикстур и parity-отчёт против PyTorch         |
| `SPEC.md`   | спецификация пайплайна и контракта графа; читать до правок в Go-порте |

`SPEC.md` — источник истины по алгоритму. В Go-порте (`guard/gliner`) нельзя
«улучшать» разбиение на слова, индексы или декодирование: они привязаны к
Python-референсу, и `SPEC.md` описывает каждый подводный камень.

## PIN-схема

Схема зафиксирована константами в начале `export.py`:

```python
ENTITY_LABELS = ["person", "email", "phone"]
ENTITY_THRESHOLD = 0.4
CLASSIFICATION_TASKS = [("safety", ["safe", "unsafe"])]
```

Число меток сущностей — статическая размерность графа. Смена списка меток или
задач классификации требует повторного экспорта и обновления фикстур. Relations,
JSON-структуры, чанкинг и attribute groups сознательно не поддерживаются.

## Пересборка модели

Нужны Python, `gliner2`, `torch`, `numpy`, `onnx`, `onnxruntime`.

```sh
python3 tools/gliner/export.py --out /tmp/gliner
```

Скрипт скачивает снапшот HF и пишет:

```text
/tmp/gliner/
  model/model.onnx        граф: энкодер + span-голова + count-голова + классификатор
  model/tokenizer.json    копия из снапшота HF (DebertaV2Tokenizer)
  model/config.json       метки, пороги, prefix_ids, позиции маркеров, имена и формы тензоров
  fixtures.json           золотые векторы (12 текстов) для parity-теста
  parity.json             расхождения ONNX против PyTorch
```

`--skip-fixtures` экспортирует только модель (без `fixtures.json`/`parity.json`).

Раскладываем результат в каталог моделей и в тестовые данные:

```sh
mkdir -p models/hivetrace__gliner-guard-omni
cp /tmp/gliner/model/{model.onnx,tokenizer.json,config.json} models/hivetrace__gliner-guard-omni/
cp /tmp/gliner/model/config.json guard/gliner/testdata/config.json
cp /tmp/gliner/fixtures.json guard/gliner/testdata/fixtures.json
```

`config.json` — машиночитаемый контракт: Go-порт читает из него метки, пороги,
`prefix_ids`, позиции маркеров и формы тензоров, а не хардкодит их. При смене
pin-схемы обновляются и фикстуры — parity-тест сравнивает с ними словоразбиение,
`input_ids`, индексы, три тензора ONNX и финальное декодирование.

Веса и нативные библиотеки в репозиторий не коммитятся (`models/`, `.ort/` в
`.gitignore`).

## Сборка и запуск с тегом ort

Сборка по умолчанию нативных библиотек не требует: `guard/gliner` без тега
`ort` — заглушка fail-closed. Реальный детектор включается тегом `ort` и требует
`libtokenizers.a` при сборке и `libonnxruntime.dylib`/`.so` при запуске.

```sh
CGO_ENABLED=1 CGO_LDFLAGS="-L.ort/lib" go build -tags ort ./...
```

- `-L.ort/lib` — каталог с `libtokenizers.a`.
- `KIT_ORT_LIB` — путь к разделяемой библиотеке ORT; по умолчанию
  `.ort/lib/libonnxruntime.dylib` (darwin) или `.ort/lib/libonnxruntime.so`.
- `KIT_MODELS_DIR` — корень моделей; по умолчанию `./models`. Модель укладывается
  как `<dir>/hivetrace__gliner-guard-omni/`.

Parity-тест (переигрывает все фикстуры через внутренности детектора; пропускается,
если модели или библиотеки ORT нет на машине):

```sh
CGO_LDFLAGS="-L.ort/lib" go test -tags ort ./guard/gliner/... -v
```

Ожидаемый результат — `12/12 fixtures passed`.

## Смотрите также

- [`SPEC.md`](./SPEC.md) — спецификация пайплайна, контракта графа и подводных камней
- [Guardrails](../../docs/concepts/guardrails.md) — слой L2 в конвейере
- [Устройство: архитектура](../../docs/internals/architecture.md) — ORT-сессии, каталог моделей, сборка
