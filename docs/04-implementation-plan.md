# 04. План реализации

Порядок этапов — от спайка GLiNER2 к ядру, guard, ES и примеру. Критерии приёмки, тесты и риски выведены из `00-README.md` (§4 журнал решений, §6 допущения A1–A9), `01-architecture.md` (§2 правила зависимостей, §3 библиотеки, §4 рантайм, §5 безопасность) и `02-business-logic.md`.

**Режим сборки.** Сейчас реализуется Этап 1 на **чистом Go**. Всё, что требует cgo/ONNX (`internal/ort`, локальный эмбеддер, ML-слой guard), стоит за интерфейсами: заглушки активны без тега `ort` (`//go:build !ort`), реальные реализации появятся под тегом `ort`. Поэтому сборка по умолчанию (`go build ./...` без тега) **не требует нативных библиотек**. Заглушки не дают ML-функциональности: `guard/gliner` возвращает `ErrGuardUnavailable` (fail-closed, BR-06), `embed.NewLocal` — ошибку конфигурации. При этом детерминированный слой guard (L1, `guard/rules`) полностью работоспособен в сборке по умолчанию. Полный режим — сборка с тегом `ort` и образом с `libonnxruntime.so`.

## 1. Этап 0 — спайк GLiNER2-omni

Цель: снять главный риск проекта (00 §6 A1) до написания ядра.

| Что           | Детали                                                                                                             | Источник |
| ------------- | ------------------------------------------------------------------------------------------------------------------ | -------- |
| Паритет       | Go/ORT против Python-референса GLiNER2 на `pii-bench` и `insecure-prompts`                                         | 00 §6 A1 |
| Латентность   | p50 на CPU (ориентир p50 ≤ 150 мс, p95 ≤ 400 мс при ≤ 512 токенов, 4 vCPU)                                         | A2       |
| Порт кода     | сборка схемы-промпта и декодирование спанов GLiNER2 на Go; метки и формат — из карточки модели и референсного кода | A1       |
| Токенайзер    | `daulet/tokenizers` отдаёт offsets для mDeBERTa/XLM-R                                                              | A4       |
| Размер модели | зафиксировать фактически: 307M по карточке HF против 209M в статье                                                 | A3       |

**Критерий приёмки:** расхождение спанов Go/ORT против Python ≤ порога, зафиксированного командой (числовой порог в источниках отсутствует — задаётся командой на Этапе 0); p50 latency измерен и занесён в документ.

**При провале:** пересмотр D6/D7 (несколько guard-провайдеров или другой рантайм), фиксируется как изменение журнала решений.

## 2. Этап 1 — ядро

Пакеты (01 §2):

| Пакет           | Роль                                                                                | Источник |
| --------------- | ----------------------------------------------------------------------------------- | -------- |
| `kit` (фасад)   | `New`, `Setup`, `Chat`, `Ingest`, `Search`, `Delete`, `Score`, `Generate[T]`        | 01 §2    |
| `internal/call` | единый примитив вызова: `authz → guard → span → fn`                                 | D1       |
| `internal/ort`  | пул ONNX-сессий + токенайзер (cgo)                                                  | 01 §2    |
| `authz`         | `policy.yaml`, `Check(principal, permission)`                                       | 01 §2    |
| `guard`         | L0, L1 (`rules/`), `Detector`, `gliner/`                                            | 01 §2    |
| `model`         | `Message`, `Client`, `openai.go`, `vllm.go`, `ollama.go`, `gigachat.go`             | 01 §2    |
| `embed`         | `Embedder`, `local.go`, `openai.go`, `gigachat.go`, `presets.go`                    | 01 §2    |
| `chunk`         | токенный чанкер                                                                     | 01 §2    |
| `store`         | ES interface, `schema.go`, `chunks.go`, `search.go`                                 | 01 §2    |
| `trace`         | модель Langfuse, async writer, read API                                             | 01 §2    |
| `cmd/kitctl`    | `model pull`, `policy check`                                                        | 01 §2    |
| `demo`          | пример: Clean Architecture сервис (`cmd/bot` + `internal/{domain,usecase,adapter}`) | 01 §2    |
| `deploy`        | `docker-compose.yml`, `grafana/`                                                    | 01 §2    |
| `testdata`      | золотые файлы, parity-фикстуры                                                      | 01 §2    |

Провайдеры: одно OpenAI-совместимое ядро плюс явные конструкторы `VLLM` (тонкая обёртка над `openai`), `Ollama` (native `/api/chat`), `OpenAI`, `GigaChat` (OAuth, свой CA) — D13. Эмбеддер: `local.go` (ONNX-энкодер), `openai.go`, `gigachat.go` — D8.

Правила зависимостей (01 §2), проверяются линтером импортов в CI:

1. `kit` импортирует всё; `internal/call` — только `authz`, `guard`, `trace`.
2. `authz`, `guard`, `model`, `embed`, `chunk`, `store` не импортируют друг друга.
3. Исключения: `trace → store`, `guard → internal/ort`, `embed → internal/ort`.
4. Глобального состояния нет, `init()` не используется.

**Критерии приёмки:**

- `go build ./...` без тега `ort` проходит и не требует нативных библиотек; линтер импортов проходит.
- `New` собирает инстанс из переданных ES-клиента (`store.ES{Do(...)}`, D10), LLM, эмбеддера и `policy.yaml`; ошибка конфигурации → `ErrInvalidConfig` (BR-08).
- Четыре адаптера LLM проходят httptest-тесты на своём wire-формате (OpenAI-compatible, `/api/chat` Ollama, OAuth GigaChat).
- `authz.Check` разбирает `policy.yaml` в строгом режиме.
- Tools: генерация JSON Schema из Go-структуры, валидация args со `additionalProperties:false`, лимит 32 KB, `max_steps`/`max_calls`/таймаут (BR-15…BR-22).
- Эмбеддер: `Query()`/`Passages()` (BR-34), пресеты e5/bge-m3 и `kit_embedder.json` (BR-35).
- `chunk` даёт токенные чанки с overlap (BR-36).
- `internal/call` создаёт observation; трейс-писатель пишет в очередь (BR-40, BR-42, BR-44).
- `errors.go` содержит sentinel `ErrUnauthorized`, `ErrGuardReject`, `ErrGuardUnavailable`, `ErrProvider`, `ErrToolNotFound`, `ErrInvalidArgs`, `ErrMaxSteps`, `ErrNotFound`, `ErrInvalidConfig`.

**«Ядро готово»:** фасад, примитив вызова, `authz`, `model`, `embed`, `chunk`, `store`-интерфейс и `trace` покрыты unit-тестами и компилируются без нативных библиотек; guard и локальный эмбеддер стоят за интерфейсами с заглушками под `ort`.

## 3. Этап 2 — guard

Четыре рельса без dialog (01 §5), детерминированный слой первым (BR-24…BR-33):

- `input` — L0/L1 (фразы, regex, контрольные суммы RU PII), затем L2 gliner; fast-fail на reject.
- `execution` — RBAC и JSON Schema аргументов, без ML (BR-03).
- `retrieval` — ACL-фильтр tenant + roles (BR-12, BR-13).
- `output` — маскирование секретов и PII.

**Критерии приёмки:** вердикты rail'ов совпадают с матрицей из `02-business-logic.md` §4; reject возвращает `ErrGuardReject` до целевого вызова (BR-25); при недоступности guard — `ErrGuardUnavailable` (BR-06, BR-29); ML-слой доступен только под тегом `ort`, тесты используют fake `guard.Detector`; LRU-кэш вердиктов работает (BR-33); карантин чанков при ingest и guard результатов tools включены (BR-31, BR-32).

## 4. Этап 3 — ES

- Индекс создаётся автоматически; размерность `dense_vector` берётся из эмбеддера; поле `acl` типа `keyword` (BR-38).
- Гибридный поиск: BM25 + kNN, оба через `_msearch` с ACL-фильтром; RRF считается в Go (D11, BR-12, BR-37).
- Трейсы по модели Langfuse: trace → observations (SPAN, GENERATION, EVENT) → scores, session как группировка (01 §6); data stream с ILM 30 дней, маскированный текст (D12, BR-41, BR-43).

**Критерии приёмки:** индекс и маппинг создаются и проверяются против золотого JSON; поиск возвращает top-k только среди разрешённых документов; RRF-слияние покрыто unit-тестами; ES подключается исключительно через пользовательский клиент `store.ES{Do(...)}` (D10); трейсы пишутся одним документом на trace + отдельным потоком observations.

## 5. Этап 4 — пример на Clean Architecture (`demo`)

`demo/` — сквозной пример: mock-ручки авторизации выдают bearer-токен, защищённая ручка запускает чат с двумя tools (погода через внешний API и роль вызывающего) и минимальным серверным системным промптом. Слои: `cmd/bot` → `internal/{domain,usecase}` → `internal/adapter/{httpapi,kitengine,weather,memory,elastic}`; `kit` импортирует только `kitengine`. HTTP/JSON на 8080 (01 §1, 03 §4).

**Критерии приёмки:** `POST /auth/login` выдаёт токен, без токена `/bot/ask` возвращает 401; с токеном `admin` модель получает оба инструмента, с `user` — только `get_weather` (RBAC из `policy.yaml`); ответ без инструментов — обычный ответ модели; трейс и observations попадают в ES. Unit-тесты слоя usecase проходят с fake-адаптерами, сервис собирается без нативных библиотек (ML-слой guard — заглушка), полный режим — образ с `libonnxruntime.so` и сборка с тегом `ort`.

## 6. Тесты

Тестовые зависимости — только stdlib (`testing`, `net/http/httptest`, `encoding/json`) (01 §3). Золотые файлы и parity-фикстуры лежат в `testdata/`.

| Этап | Что проверяется                                                                                                                                        | Тип                                                                   |
| ---- | ------------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------- |
| 0    | паритет Go/ORT против Python по спанам на `pii-bench` и `insecure-prompts`; p50 latency                                                                | parity + замер (внешний Python-скрипт, не зависимость репозитория)    |
| 1    | адаптеры LLM (wire-формат), `authz.Check`, генерация схем и валидация args, лимиты, чанкер, пресеты эмбеддера, батчинг трейс-писателя, sentinel-ошибки | unit, золотые файлы                                                   |
| 2    | вердикты rail'ов по матрице, fast-fail, fail-closed, LRU-кэш, карантин, guard результатов tools, fake `Detector`                                       | unit, золотые пары                                                    |
| 3    | маппинг индекса, тело `_msearch` для kNN и BM25, RRF, ACL-фильтр, data stream + ILM                                                                    | unit (золотые JSON) + интеграционные через клиент `store.ES{Do(...)}` |
| 4    | ingest → search → chat с tools, сквозной ACL                                                                                                           | интеграционные, `httptest` с fake-провайдерами                        |

Интеграционные тесты ES выполняются через клиент, который пишет пользователь (D10); для unit-уровня достаточно fake-реализации `store.ES`.

## 7. Риски и меры

| Риск                                                                          | Влияние                                 | Мера                                                                                                                                                     | Этап |
| ----------------------------------------------------------------------------- | --------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------- | ---- |
| Неофициальный ONNX-экспорт GLiNER2: references и postprocessing не поддержаны | ML-слой guard может не собраться        | паритет-спайк Go/ORT против Python; порт схемы-промпта и декодирования спанов; при провале — пересмотр D6/D7                                             | 0    |
| Расхождение размеров модели: 307M (карточка) против 209M (статья)             | неверная оценка памяти и latency        | зафиксировать фактические веса и память, уточнить ресурсы `demo` (4 vCPU / 4 GB)                                                                         | 0    |
| Latency guard на CPU                                                          | выход за p50 ≤ 150 мс / p95 ≤ 400 мс    | int8 при прохождении parity, пул сессий и `intra_op_threads`, LRU-кэш вердиктов, лимит `max_chars`                                                       | 0, 2 |
| Отсутствие RRF на basic-лицензии ES                                           | гибридный поиск недоступен «из коробки» | RRF считаем в Go по `_msearch`; проверить `_msearch`+knn на basic                                                                                        | 3    |
| GigaChat OAuth и самоподписанные сертификаты                                  | адаптер не заработает как drop-in       | отдельный адаптер, порт 9443 OAuth + свой CA, structured output через function calling; сверка с документацией Sber                                      | 2    |
| cgo и `libtokenizers.a` в сборке                                              | усложнение Dockerfile и dev-сборки      | builder ставит `libtokenizers.a`, runtime копирует `libonnxruntime.so` (`KIT_ORT_LIB`); сборка по умолчанию без тега `ort` нативных библиотек не требует | 1, 4 |

## 8. Definition of Done v1

- [ ] `go build ./...` без тега `ort` не требует нативных библиотек; линтер импортов проходит (01 §2).
- [ ] Все unit-тесты (stdlib) проходят.
- [ ] Паритет guard на `pii-bench` и `insecure-prompts` — в пределах порога команды.
- [ ] Guard: четыре рельса (input, retrieval, execution, output), детерминированный слой первым, fail-closed.
- [ ] Tools: вычитание запрещённых из схемы, повторная проверка при исполнении, лимиты `max_steps`/`max_calls`/таймаут, args ≤ 32 KB.
- [ ] ES: авто-создание индекса, `dense_vector` из эмбеддера, `acl` keyword, ACL-фильтр в kNN и BM25, RRF в Go.
- [ ] Трейсы: модель Langfuse, data stream + ILM 30 дней, маскированный текст, `TraceStoreRaw` выключен.
- [ ] `demo`: mock-авторизация, чат с tools (погода и роль), RBAC по ролям.
- [ ] `deploy/`: `docker-compose.yml` + Grafana provisioning и дашборды (латентность/ошибки, токены/стоимость, срабатывания guard, топ tools и отказы авторизации, последние trace).
- [ ] `cmd/kitctl`: `model pull`, `policy check`.
- [ ] Dockerfile: builder (`golang:1.24-bookworm`) + runtime (`debian:bookworm-slim` + `libonnxruntime.so`), non-root, read-only rootfs, `/models` read-only.
- [ ] Документы `02`, `03`, `04` и `demo/README.md` актуальны.
