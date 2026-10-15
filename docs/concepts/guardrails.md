# Guardrails

Guardrail — конвейер проверок текста, который решает, пропустить его, замаскировать найденное или отклонить операцию. Детерминированный слой выполняется раньше ML, а на вердикте reject конвейер останавливается сразу.

## Когда использовать

- Модель принимает недоверенный ввод: запрос пользователя, документ, результат инструмента.
- Нужно необратимо маскировать PII и секреты до того, как они попадут в модель.
- Нужно отклонять инъекции и вредный контент до вызова модели или инструмента.

## Быстрый старт

Файл: `demo/internal/adapter/kitengine/engine.go`

```go
app, err := kit.New(ctx, kit.Config{
	ES:      es,
	LLM:     llm,
	Policy:  policy,
	GuardL2: guardL2, // guard.Detector: ML-слой; без него работает только L0/L1
})
if err != nil {
	return err
}
```

Детерминированный слой L0/L1 подключается всегда (`guard/rules`). Поле `GuardL2` добавляет ML-модель за интерфейсом `guard.Detector`.

## Как использовать

### Рельсы

Рельсы — константы пакета `guard`: `RailInput` (`input`), `RailRetrieval` (`retrieval`), `RailExecution` (`execution`), `RailOutput` (`output`), `RailToolResult` (`tool_result`).

### Слои и порядок

Конвейер идёт «дёшево → дорого»: L0 → L1 → L2.

- L0/L1 — детерминированный слой `guard/rules`: фразы, regex, контрольные суммы RU PII (СНИЛС, ИНН, ОГРН). Подключается всегда.
- L2 — ML-модель за интерфейсом `guard.Detector`. В v1 одна модель: ONNX-экспорт
  `hivetrace/gliner-guard-omni` (см. ниже). В сборке без тега `ort` ML-слой —
  заглушка fail-closed.

### Слой L2 (gliner)

L2 — пакет `guard/gliner` под тегом сборки `ort`: ONNX-модель через ONNX Runtime,
без Python в рантайме. Веса и токенайзер лежат в
`<models_dir>/hivetrace__gliner-guard-omni/` (`model.onnx`, `tokenizer.json`,
`config.json`). Пайплайн: нормализация текста → regex-разбиение на слова →
токенизация по словам → тензоры индексов → ONNX → декодирование спанов и
классификации. Спецификация пайплайна и контракта графа —
[`tools/gliner/SPEC.md`](../../tools/gliner/SPEC.md).

Схема зафиксирована на экспорте: три метки сущностей (`person`, `email`,
`phone`) и одна классификация `safety` (`safe`/`unsafe`). Число меток —
статическая размерность графа, поэтому смена схемы требует повторного экспорта
([`tools/gliner/README.md`](../../tools/gliner/README.md)). Метки, пороги и
`prefix_ids` Go читает из `config.json`, а не хардкодит. Атаки, вредный контент,
intent и tone модель не размечает — только PII-сущности и `safety`.

Находка в `guard.Finding`:

| Что нашла модель                  | `Category` | `Label`        | `Start`/`End`          |
| --------------------------------- | ---------- | -------------- | ---------------------- |
| сущность `person`/`email`/`phone` | `PII`      | метка сущности | границы спана          |
| вердикт `unsafe`                  | `UNSAFE`   | `unsafe`       | весь текст (`0`…длина) |

`Start`/`End` — смещения в рунах во входной текст детектора. У вердикта
`unsafe` спана нет, поэтому он покрывает весь текст. `Score` — вероятность
модели, `Layer` — `L2`. Безопасный вердикт находки не даёт.

Включение:

```go
l2, err := gliner.New(gliner.Config{Model: "hivetrace__gliner-guard-omni"})
if err != nil {
	return err // fail-closed
}
app, err := kit.New(ctx, kit.Config{ES: es, LLM: llm, Policy: policy, GuardL2: l2})
```

`Config.Dir` по умолчанию берётся из `KIT_MODELS_DIR`, затем `./models`; пул
сессий — `Config.Sessions` (по умолчанию 2); ожидание свободной сессии —
`Config.Wait` (по умолчанию 2 с). Любая ошибка модели — нет каталога, не поднялся
ORT, плохой тензор, таймаут очереди — возвращается как `ErrGuardUnavailable`:
операция отклоняется, а не пропускается. Пустой текст не даёт ни находок, ни
ошибки.

Без тега `ort` тот же пакет собирается как заглушка: `Detect` возвращает
`ErrGuardUnavailable` (fail-closed).

### Fast-fail и fail-closed

При reject в L1/L2 дальнейшие слои и целевой вызов не выполняются. Если guard недоступен, операция отклоняется (`ErrGuardUnavailable`), а не пропускается.

### Действия и категории

Результат проверки — `Verdict` с действием `Allow`, `Mask` или `Reject`. Категории находок: `PII`, `SECRET`, `INJECTION`, `UNSAFE`. Вне рельса `output` инъекции и unsafe дают reject; PII и секреты маскируются всегда.

### Маскирование PII

Маскирование необратимо: модель видит заглушки вида `[PII:SNILS]`, и реальные значения не попадают в аргументы инструментов.

### Результаты инструментов

Результат инструмента проверяется как недоверенный ввод на рельсе `tool_result`. При reject результат заменяется на `[blocked by guard]`.

### Лимиты и кэш

- `GuardMaxChars` (по умолчанию 8000) — вход обрезается по числу рун.
- `GuardCacheSize` (по умолчанию 4096) — LRU-кэш вердиктов; ключ — рельс плюс текст.
- Для локальной разработки есть детектор `guard.AllowAll()`, который отключает fail-closed.

### Рельсы: что проверяется и действие

| Rail          | Что проверяется                                                                                            | Действие                                                                                                                      |
| ------------- | ---------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------- |
| `input`       | L0/L1: фразы, regex, контрольные суммы RU PII. L2: `safety` и PII-сущности (person, email, phone)          | атаки и unsafe → reject; PII → mask необратимо; иначе allow                                                                   |
| `retrieval`   | ACL-фильтр tenant + roles внутри kNN и BM25, deny-by-default; карантин чанков при ingest                   | чанк вне ACL не возвращается; отклонённый чанк — в карантин                                                                   |
| `execution`   | RBAC, JSON Schema со `additionalProperties: false`, лимиты args, steps, calls, timeout; ML не используется | нет прав → `ErrUnauthorized`; неизвестный tool → `ErrToolNotFound`; невалидные args → `ErrInvalidArgs`; лимит → `ErrMaxSteps` |
| `output`      | правила маскирования секретов и PII; L2 — та же модель                                                     | найдено → mask; иначе allow                                                                                                   |
| `tool_result` | результаты инструментов проверяются как `input`, fast-fail на инъекциях                                    | reject → результат заменяется на `[blocked by guard]`; PII и секреты → mask                                                   |

## Дальше

| Цель                                  | Куда идти                           |
| ------------------------------------- | ----------------------------------- |
| Понять, где guard вызывается в цикле  | [Chat](./chat.md)                   |
| Прочитать про карантин при индексации | [Поиск](./retrieval.md)             |
| Настроить роли и права                | [Доступ](./access-control.md)       |
| Увидеть срабатывания guard в трейсах  | [Наблюдаемость](./observability.md) |

## Смотрите также

- [Конфигурация](../reference/configuration.md) — `GuardL2`, `GuardMaxChars`, `GuardCacheSize`
- [Бизнес-правила](../internals/business-rules.md) — `BR-24`…`BR-33`
- [Ошибки](../reference/errors.md) — `ErrGuardReject`, `ErrGuardUnavailable`
