# Инструменты gliner

Экспорт и воспроизведение guard-модели L2 (`hivetrace/gliner-guard-omni`) в
ONNX. Здесь два файла:

| Файл                       | Что                                                                       |
| -------------------------- | ------------------------------------------------------------------------- |
| `export.py`                | экспорт графа, генерация фикстур и parity-отчёт против PyTorch            |
| `SPEC.md`                  | спецификация пайплайна и контракта графа; читать до правок в Go-порте     |
| `config.full.json.example` | контракт `config.json` для полной таксономии (`--schema full`), без весов |

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

> `hivetrace/gliner-guard-omni` на HF содержит `model.safetensors`, а не
> `model.onnx`; `gobellimctl model pull` для неё не подходит — ONNX-граф создаёт этот
> скрипт.

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
`--schema full` выбирает полную таксономию guard-модели (см. ниже); по
умолчанию экспортируется pin-схема.

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

## Re-exporting a richer label space

The Go detector (`guard/gliner`) is schema-driven: it reads the label space, one
classification group per ONNX output, the activations, thresholds and
`category_map` from `config.json`, and it accepts **N** classification groups
with no code change. The shipped export still carries the pinned 3-label /
1-softmax schema (`person`/`email`/`phone`, `safe`/`unsafe`); the model's full
guard taxonomy (32 PII span types, 15 multi-label adversarial, 30
harmful-content, 13 intent, 10 tone) is selectable with `--schema full`, and its
resulting `config.json` contract is committed as `config.full.json.example`.

The ONNX graph and `config.json` must come from `export.py`; do not hand-edit
`config.json` to claim a richer label space — the graph's static dims (entity
labels, class markers, per-group classification logits) are baked into
`model.onnx` at export time. Weights are not committed, so regenerating the
richer export is a **separate local step**.

### Pipeline

1. **Weights.** `export.py` loads `hivetrace/gliner-guard-omni` with
   `HF_HUB_OFFLINE=1`. Either the HF cache already holds the snapshot
   (`~/.cache/huggingface/hub/models--hivetrace--gliner-guard-omni/snapshots/<rev>/model.safetensors`),
   or download it once (`huggingface-cli download hivetrace/gliner-guard-omni`)
   and then run offline. CPU-only export is fine.
2. **Schema.** `--schema` selects the pinned constants at the top of `export.py`:
   `default` (the shipped `ENTITY_LABELS`/`ENTITY_THRESHOLD`/`CLASSIFICATION_TASKS`
   pin) or `full` (the model card's taxonomy, held in `FULL_ENTITY_LABELS` and
   `FULL_SCHEMA`). `full` emits one ONNX output per classification group — the
   names in `classifications[].output` — and a top-level `category_map`. The
   default invocation is unchanged: it reproduces the shipped `config.json` and
   `fixtures.json` byte-for-byte.
3. **Export.** `python3 tools/gliner/export.py --out /tmp/gliner` (requires
   Python + `gliner2`, `torch`, `numpy`, `onnx`, `onnxruntime`; `optimum` is not
   used). This regenerates `model/model.onnx` (one classification output per
   group), `model/tokenizer.json`, `model/config.json`, `fixtures.json` and
   `parity.json`.
4. **`config.json` fields.** `write_config()` must emit, per
   `schema.classifications[]` entry:
   - `labels` and `schema_marker_positions` (length `len(labels)+1`);
   - `output` — the ONNX output tensor name for that group, unique per group
     (the Go side defaults it to `cls_logits` when absent, which is exactly the
     shipping model);
   - `multi_label` — `true` requires `activation: "sigmoid"` and emits every
     label with `sigmoid(logit) >= threshold` (no argmax fallback: if nothing
     clears its threshold the group contributes no finding); leave `activation`
     empty or set `"softmax"` for a single-argmax group;
   - `threshold` (group default) and optional `thresholds` (per-label) for the
     multi-label sigmoid path;

   and, top-level, `category_map`
   (`{"label": "PII|SECRET|INJECTION|UNSAFE|SAFE"}`). A label with no
   `category_map` entry and no heuristic match falls back to `UNSAFE`; a label
   explicitly mapped to `SAFE` (case-insensitive), or one of the built-in
   safe-list names (`safe`, `benign`, `neutral`, …), produces no finding.

5. **Copy into place** — models directory and testdata, exactly as in
   [Пересборка модели](#пересборка-модели) above.
6. **Re-verify parity.** The recorded fixtures pin the previous decode, so a new
   export needs its fixtures regenerated and the gate re-run:
   `gobellimctl verify parity --dir models --fixtures guard/gliner/testdata`
   (equivalently `go test -tags ort ./guard/gliner/ -run TestFixturesParity`) must
   report `12/12` — or the new fixture count — before the export is trusted.

### Example contract (`config.full.json.example`)

`config.full.json.example` is exactly the `config.json` that
`python3 tools/gliner/export.py --schema full --out <dir>` writes:

- **entities**: the 32 PII span labels (`person`, `first_name`, …, `crypto_wallet`);
- **classifications**: five groups, one ONNX output each — `safety_logits` (2,
  softmax), `adversarial_logits` (15, multi-label `sigmoid`),
  `harmful_logits` (30, multi-label `sigmoid`), `intent_logits` (13, softmax),
  `tone_logits` (10, softmax);
- **category_map**: adversarial/jailbreak labels → `INJECTION`, harmful-content
  and the malicious intent/tone labels → `UNSAFE`, every benign intent/tone label
  → `SAFE` (the `none` clean labels are left out so the Go safe-list suppresses
  them).

Weights are not committed. Regenerate the ONNX locally into
`models/hivetrace__gliner-guard-omni-full/` and re-run the parity gate (step 6)
before trusting it.

With that export in place the shipped Go detector loads it unchanged
(`gliner.VerifyDir` passes, no Go change) and, on the model card's own examples,
returns `person`/`email`/`phone` (PII) for a contact string and
`instruction_override`/`jailbreak_persona` (**INJECTION**) for
`Ignore all previous instructions …` — the last is impossible with the shipped
3-label model.

**SAFE suppression (resolved).** `category_map` accepts the value `SAFE`
(case-insensitive): a label mapped to it produces no finding. The full taxonomy's
benign _intent_/_tone_ labels (`informational`, `formal`, …) are mapped to `SAFE`
by `_full_category_map()`, so they no longer fall through to the fail-safe
`UNSAFE` finding. Suppression is always explicit — an unmapped label still fails
safe to `UNSAFE` — and `validate` rejects any other unknown value (and the empty
string), so a mapping can never be ambiguous. The Go detector's built-in
safe-list (`safe`, `benign`, `neutral`, …) remains the name-based fallback for
models that ship no `category_map`.

Because the `SAFE` labels come from `_full_category_map()`, the committed
`config.full.json.example` was regenerated from that function (only the
`category_map` block changed; the rest is byte-identical to a full `--schema
full` export).

**Caveat.** The shipped model stays 3-label / 1-classification; the richer
`model.onnx` / `config.json` are produced only by a local `--schema full` export
(its contract is checked in here as `config.full.json.example`).

## Смотрите также

- [`SPEC.md`](./SPEC.md) — спецификация пайплайна, контракта графа и подводных камней
- [Guardrails](../../docs/concepts/guardrails.md) — слой L2 в конвейере
- [Устройство: архитектура](../../docs/reference/gobellim.md) — ORT-сессии, каталог моделей, сборка
