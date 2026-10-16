# План

Порядок этапов — от спайка GLiNER2 к ядру, guard, ES и примеру. Критерии
приёмки, тесты и риски выведены из [Решений и допущений](./decisions.md) (§4
журнал решений, §6 допущения `A1`…`A9`), [Архитектуры](./architecture.md) (§2
правила зависимостей, §3 библиотеки, §4 рантайм, §5 безопасность) и
[Бизнес-правил](./business-rules.md).

> [!NOTE]
> **Режим сборки.** Этап 1 реализуется на **чистом Go**. Всё, что требует
> cgo/ONNX (`internal/ort`, локальный эмбеддер, ML-слой guard), стоит за
> интерфейсами: заглушки активны без тега `ort` (`//go:build !ort`). Поэтому
> сборка по умолчанию (`go build ./...` без тега) **не требует нативных
> библиотек**. Заглушки не дают ML-функциональности: `guard/gliner` возвращает
> `ErrGuardUnavailable` (fail-closed по умолчанию, BR-06; на рельсах из
> `Config.GuardFailOpen` ошибка ML-слоя не фатальна), `embed.NewLocal` — ошибку
> конфигурации. При этом детерминированный слой guard (L1, `guard/rules`)
> полностью работоспособен в сборке по умолчанию. Под тегом `ort` реализованы и
> ML-слой guard (`guard/gliner`, ONNX-only; parity-тест на 12/12 фикстур), и
> локальный эмбеддер (`embed.NewLocal`, ONNX-энкодер). Полный режим — сборка с
> тегом `ort` и образом с `libonnxruntime.so`.

## Этап 0 — спайк GLiNER2-omni

Цель: снять главный риск проекта (`A1`) до написания ядра.

| Что           | Детали                                                                                                                        | Источник   |
| ------------- | ----------------------------------------------------------------------------------------------------------------------------- | ---------- |
| Паритет       | Go/ORT против Python-референса GLiNER2 на `pii-bench` и `insecure-prompts`                                                    | решения A1 |
| Латентность   | p50 на CPU; SLO — короткий ввод p50 ≤ 250 мс / p95 ≤ 400 мс, полный тензорный вход — p50 ≤ 800 мс за окно детекции (D53, D60) | A2, D53    |
| Порт кода     | сборка схемы-промпта и декодирование спанов GLiNER2 на Go; метки и формат — из карточки модели и референсного кода            | A1         |
| Токенайзер    | `daulet/tokenizers` отдаёт offsets для mDeBERTa/XLM-R                                                                         | A4         |
| Размер модели | зафиксировать фактически: 307M по карточке HF против 209M в статье                                                            | A3         |

**Критерий приёмки:** расхождение спанов Go/ORT против Python ≤ порога,
зафиксированного командой (числовой порог в источниках отсутствует — задаётся
командой на Этапе 0); p50 latency измерен и занесён в документ.

**При провале:** пересмотр D6/D7 (несколько guard-провайдеров или другой
рантайм), фиксируется как изменение журнала решений.

## Этап 1 — ядро

Пакеты (архитектура):

| Пакет                  | Роль                                                                                                                        | Источник    |
| ---------------------- | --------------------------------------------------------------------------------------------------------------------------- | ----------- |
| `kit` (фасад)          | `New`, `Setup`, `Chat`, `Ingest`, `Search`, `Delete`, `Score`, `Generate[T]`                                                | архитектура |
| `internal/call`        | единый примитив вызова: `authz → guard → span → fn`                                                                         | D1          |
| `internal/ort`         | пул ONNX-сессий (cgo; токенайзера здесь нет)                                                                                | архитектура |
| `authz`                | `policy.yaml`, `Check(principal, permission)`                                                                               | архитектура |
| `guard`                | `Rail`/`Detector`/`Pipeline`, `canonical.go`; L0, L1 (`rules/`), `gliner/`                                                  | архитектура |
| `model`                | `Message`, `Client`, `openai.go`, `vllm.go`, `ollama.go`, `gigachat.go`                                                     | архитектура |
| `embed`                | `Embedder`, `local.go`, `openai.go`, `gigachat.go`, `presets.go`                                                            | архитектура |
| `chunk`                | токенный чанкер; токенайзер — интерфейс, по умолчанию `WhitespaceTokenizer`                                                 | архитектура |
| `store`                | ES interface + `Request`, `schema.go`/`index.go`, `chunks.go`, `search.go`, `filter.go`, `maintenance.go`                   | архитектура |
| `trace`                | модель [Langfuse](https://langfuse.com/), async writer, политика текста и sampling (read API — запланирован, не реализован) | архитектура |
| `eval`                 | детерминированные проверки трейсов (stdlib; зависит от `trace`, не от `kit`)                                                | дизайн D30  |
| `flow`                 | типизированные последовательные step-пайплайны: schema-контракт и suspend/resume                                            | дизайн D31  |
| `webfetch`             | SSRF-защищённый инструмент исходящего HTTP (эффект egress)                                                                  | дизайн D33  |
| `internal/importrules` | stdlib-тест правил импортов (I-13)                                                                                          | архитектура |
| `cmd/kitctl`           | `model pull`, `policy check`, `verify window/models/parity/latency`                                                         | архитектура |
| `demo`                 | пример: Clean Architecture сервис (`cmd/bot` + `internal/{domain,usecase,adapter}`)                                         | архитектура |
| `demo/deploy`          | `docker-compose.yml`, `grafana/`                                                                                            | архитектура |
| `testdata`             | золотые файлы, parity-фикстуры                                                                                              | архитектура |

Провайдеры: одно OpenAI-совместимое ядро плюс явные конструкторы `VLLM` (тонкая
обёртка над `openai`), `Ollama` (native `/api/chat`), `OpenAI`, `GigaChat`
(OAuth, свой CA) — D13. Эмбеддер: `local.go` (ONNX-энкодер), `openai.go`,
`gigachat.go` — D8.

Правила зависимостей (архитектура) обязательны и **проверяются автоматически**:
stdlib-тест `internal/importrules` (в `go test ./...`) и правила `depguard`
(`.golangci.yml`, в `make check` и в CI-джобе `lint`):

1. `kit` импортирует всё; `internal/call` — только `authz`, `guard`, `trace`.
2. `authz`, `guard`, `model`, `embed`, `chunk`, `store`, `trace` не импортируют друг
   друга.
3. Исключения: `trace → store`, `guard → internal/ort`, `embed → internal/ort`,
   `eval → trace`.
4. `eval` не импортирует `kit`; глобального состояния нет, `init()` не используется.

**Критерии приёмки:**

- `go build ./...` без тега `ort` проходит и не требует нативных библиотек;
  правила импортов проверяются тестом `internal/importrules` и `depguard`.
- `New` собирает инстанс из переданных ES-клиента (`store.ES`, `Do(ctx, store.Request)`, D10),
  LLM, эмбеддера и `policy.yaml`; ошибка конфигурации → `ErrInvalidConfig`
  (BR-08).
- Четыре адаптера LLM проходят httptest-тесты на своём wire-формате
  (OpenAI-compatible, `/api/chat` Ollama, OAuth GigaChat).
- `authz.Authorize` разбирает `policy.yaml` в строгом режиме.
- Tools: генерация JSON Schema из Go-структуры, валидация args со
  `additionalProperties:false`, лимит 32 KB, `max_steps`/`max_calls`/таймаут
  (BR-15…BR-22).
- Эмбеддер: `Query()`/`Passages()` (BR-34), пресеты e5/bge-m3 и
  `kit_embedder.json` (BR-35).
- `chunk` даёт токенные чанки с overlap (BR-36).
- `internal/call` создаёт observation; трейс-писатель пишет в очередь (BR-40,
  BR-42, BR-44).
- `errors.go` содержит sentinel `ErrUnauthorized`, `ErrGuardReject`,
  `ErrGuardUnavailable`, `ErrProvider`, `ErrToolNotFound`, `ErrInvalidArgs`,
  `ErrMaxSteps`, `ErrNotFound`, `ErrInvalidConfig`, `ErrTool`, `ErrBudget`,
  `ErrRateLimited`, `ErrApprovalRequired`.
- Список sentinel’ов закрыт: `ErrTool` (`kit.go`, обёртка ошибки обработчика
  инструмента), `ErrToolNotFound` (`calltool.go`, `internal/call`) и `ErrNotFound`
  (`internal/call`, `store`, `trace`, адаптеры `demo`) больше не числятся
  зарезервированными — у каждого есть производственный сайт выдачи и запись в
  `docs/reference/errors.md`. Резервировать новые имена не требуется.

**«Ядро готово»:** фасад, примитив вызова, `authz`, `model`, `embed`, `chunk`,
`store`-интерфейс и `trace` покрыты unit-тестами и компилируются без нативных
библиотек; guard стоит за интерфейсом (L2 реализован под `ort`), локальный
эмбеддер — `embed.NewLocal` (тоже реализован под `ort`).

## Этап 2 — guard

Три рельса, каноникализация и decode-and-rescan перед слоями,
детерминированный слой первым (BR-24…BR-33, BR-55, BR-56):

- `input` — L1 (фразы, regex, контрольные суммы RU PII), затем L2 gliner;
  fast-fail на reject.
- `tool_result` — результаты инструментов проверяются как недоверенный ввод.
- `output` — маскирование секретов и PII; инъекция/unsafe — `Detect`, а не reject.

**Критерии приёмки:** вердикты rail'ов совпадают с матрицей из
[Бизнес-правил](./business-rules.md) §4; reject возвращает `ErrGuardReject` до
целевого вызова (BR-25); при недоступности guard — `ErrGuardUnavailable` (BR-06,
BR-29); ML-слой доступен только под тегом `ort`, тесты используют fake
`guard.Detector`; LRU-кэш вердиктов работает (BR-33); карантин чанков при ingest
и guard результатов tools включены (BR-31, BR-32).

## Этап 3 — ES

- Индекс создаётся автоматически; размерность `dense_vector` берётся из
  эмбеддера; поле `acl` типа `keyword` (BR-38).
- Гибридный поиск: BM25 + kNN, оба через `_msearch` с ACL-фильтром; RRF
  считается в Go (D11, BR-12, BR-37).
- Трейсы по модели [Langfuse](https://langfuse.com/): trace → observations (SPAN, GENERATION, EVENT) →
  scores, session как группировка (архитектура); data stream с ILM 30 дней,
  маскированный текст (D12, BR-41, BR-43).

**Критерии приёмки:** индекс и маппинг создаются и проверяются против золотого
JSON; поиск возвращает top-k только среди разрешённых документов; RRF-слияние
покрыто unit-тестами; ES подключается исключительно через пользовательский
клиент `store.ES` (`Do(ctx, store.Request)`) (D10); трейсы пишутся одним документом на trace +
отдельным потоком observations.

## Этап 4 — пример на Clean Architecture (`demo`)

`demo/` — сквозной пример: mock-ручки авторизации выдают bearer-токен,
защищённая ручка запускает чат с двумя tools (погода через внешний API и роль
вызывающего) и минимальным серверным системным промптом. Слои: `cmd/bot` →
`internal/{domain,usecase}` → `internal/adapter/{httpapi,kitengine,weather,memory,elastic}`;
`kit` импортирует только `kitengine`. HTTP/JSON на 8080 (архитектура, demo).

**Критерии приёмки:** `POST /auth/login` выдаёт токен, без токена `/bot/ask`
возвращает 401; с токеном `admin` модель получает оба инструмента, с `user` —
только `get_weather` (RBAC из `policy.yaml`); ответ без инструментов — обычный
ответ модели; трейс и observations попадают в ES. Unit-тесты слоя usecase
проходят с fake-адаптерами, сервис собирается без нативных библиотек (ML-слой
guard — заглушка), полный режим — образ с `libonnxruntime.so` и сборка с тегом
`ort`.

## Тесты

Тестовые зависимости — только stdlib (`testing`, `net/http/httptest`,
`encoding/json`) (архитектура). Золотые файлы и parity-фикстуры лежат в `testdata/`.

| Этап | Что проверяется                                                                                                                                            | Тип                                                                |
| ---- | ---------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------ |
| 0    | паритет Go/ORT против Python по спанам на `pii-bench` и `insecure-prompts`; p50 latency                                                                    | parity + замер (внешний Python-скрипт, не зависимость репозитория) |
| 1    | адаптеры LLM (wire-формат), `authz.Authorize`, генерация схем и валидация args, лимиты, чанкер, пресеты эмбеддера, батчинг трейс-писателя, sentinel-ошибки | unit, золотые файлы                                                |
| 2    | вердикты rail'ов по матрице, fast-fail, fail-closed, LRU-кэш, карантин, guard результатов tools, fake `Detector`                                           | unit, золотые пары                                                 |
| 3    | маппинг индекса, тело `_msearch` для kNN и BM25, RRF, ACL-фильтр, data stream + ILM                                                                        | unit (золотые JSON) + интеграционные через клиент `store.ES`       |
| 4    | ingest → search → chat с tools, сквозной ACL                                                                                                               | интеграционные, `httptest` с fake-провайдерами                     |

Интеграционные тесты ES выполняются через клиент, который пишет пользователь
(D10); для unit-уровня достаточно fake-реализации `store.ES`.

## Риски и меры

| Риск                                                                          | Влияние                                 | Мера                                                                                                                                                     | Этап |
| ----------------------------------------------------------------------------- | --------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------- | ---- |
| Неофициальный ONNX-экспорт GLiNER2: references и postprocessing не поддержаны | ML-слой guard может не собраться        | паритет-спайк Go/ORT против Python; порт схемы-промпта и декодирования спанов; при провале — пересмотр D6/D7                                             | 0    |
| Расхождение размеров модели: 307M (карточка) против 209M (статья)             | неверная оценка памяти и latency        | зафиксировать фактические веса и память, уточнить ресурсы `demo` (4 vCPU / 4 GB с L1; ≥6 GB с L2)                                                        | 0    |
| Latency guard на CPU                                                          | выход за SLO `D53`                      | int8 при прохождении parity, пул сессий и `intra_op_threads`, LRU-кэш вердиктов, лимит `max_chars`; при нехватке — меньшая guard-модель                  | 0, 2 |
| Отсутствие RRF на basic-лицензии ES                                           | гибридный поиск недоступен «из коробки» | RRF считаем в Go по `_msearch`; проверить `_msearch`+knn на basic                                                                                        | 3    |
| GigaChat OAuth и самоподписанные сертификаты                                  | адаптер не заработает как drop-in       | отдельный адаптер, порт 9443 OAuth + свой CA, structured output через function calling; сверка с документацией Sber                                      | 2    |
| cgo и `libtokenizers.a` в сборке                                              | усложнение Dockerfile и dev-сборки      | builder ставит `libtokenizers.a`, runtime копирует `libonnxruntime.so` (`KIT_ORT_LIB`); сборка по умолчанию без тега `ort` нативных библиотек не требует | 1, 4 |

## Definition of Done v1

Отметка `[x]` — поведение подтверждено кодом и прогоном
`go build ./... && go vet ./... && go test ./...`; пустой чекбокс — открыто.

- [x] `go build ./...` без тега `ort` проходит и не требует нативных библиотек.
- [x] Линтер импортов и CI подключены: `.github/workflows/ci.yml` (build, test,
      race, ort-vet, lint, fumpt), `.golangci.yml` (`gofumpt`/`govet`/`errcheck`/
      `staticcheck`/`gosec`/`depguard`), stdlib-тест `internal/importrules` и `make
check`. Обвязка зелёная: прогоны на раннере проходят (push в `develop`/`main`
      и pull request), `golangci-lint` v2.13.0 локально даёт 0 замечаний, гейт
      паритета `kitctl verify parity` работает в ORT-сборке.
- [x] Все unit-тесты (stdlib) проходят.
- [ ] Паритет guard на `pii-bench` и `insecure-prompts` — численный порог не
      подтверждён; parity-фикстуры `guard/gliner` проходят 12/12 под `ort`.
      Измеренные числа L1 по публичному корпусу зафиксированы в
      [Как настроить guardrails](../guides/guardrails.md#качество-l1-измеренные-числа)
      — с явной оговоркой, что они не доказывают обобщаемость (там же результат
      пробы на held-out). Остаётся гейт паритета L2: он требует `-tags ort` и
      веса ONNX, которых нет в git.
- [x] Guard: рельсы `input`, `output`, `tool_result`;
      каноникализация и decode-and-rescan перед слоями; детерминированный слой
      первым; fail-closed; действие `Detect` на выходе.
- [x] Tools: вычитание запрещённых из схемы, повторная проверка при исполнении,
      лимиты `max_steps`/`max_calls`/таймаут, args ≤ 32 KB, эффекты
      (egress/approval), бюджет хода.
- [x] ES: авто-создание индекса, `dense_vector` из эмбеддера, `acl` keyword,
      ACL-фильтр и `quarantined:false` в kNN и BM25, фильтр `store.Filter`, RRF
      в Go.
- [x] Трейсы: модель [Langfuse](https://langfuse.com/), data stream + ILM 30 дней, политика текста
      `masked` по умолчанию с opt-in `full`, аудит-поля, детерминированный
      sampling, export-лимиты и processors.
- [x] `demo`: mock-авторизация, чат с tools (погода и роль), RBAC по ролям.
- [x] `demo/deploy/`: `docker-compose.yml` + Grafana provisioning и дашборды
      (латентность/ошибки, токены/стоимость, срабатывания guard, топ tools и
      отказы авторизации, последние trace).
- [x] `cmd/kitctl`: `model pull`, `policy check`, `verify window/models/parity/latency`.
- [x] Dockerfile: builder (`golang:1.24-bookworm`) + runtime
      (`debian:bookworm-slim`), non-root, read-only rootfs; ORT-вариант требует
      копии `libonnxruntime.so` и `KIT_ORT_LIB`.
- [x] Пакет `eval`: детерминированные проверки трейсов (stdlib, зависит от
      `trace`, не от `kit`).
- [x] Судьба `flow` и `eval` зафиксирована: оба остаются в ядре (D67). Измерено
      `go list -deps`: у `eval` внешних зависимостей нет, у `flow` — две
      jsonschema-библиотеки ради схем `In`/`Out`, то есть «нулевые зависимости»
      верны только для `eval`. Ограничение `eval` держит правило
      `eval-trace-and-internal-only` из `internal/importrules`; для `flow` такого
      правила нет.
- [x] Локальный эмбеддер (`embed.NewLocal`) реализован под `ort`: пресеты
      e5/bge-m3, `kit_embedder.json`, батч 32, `LocalConfig.Sessions`.
- [x] Guard-защита от сверхлинейной стоимости: у L2 есть потолок токенов
      (`gliner.Config.MaxTokens`, дефолт 512) — стоимость L2 стала плоской по
      длине входа (D34).
- [x] Хуки, approval, structurer, flow, webfetch: `BeforeToolCall`/`AfterToolCall`
      (+ app-level `Config.BeforeToolCall`/`AfterToolCall`, guard результата — последним),
      approval с fingerprint и consume-once, `GenerateRequest.StructuringModel`
      (`model.Structurer`), типизированный `flow` с suspend/resume, SSRF-защищённый
      `webfetch`.
- [x] Замер латентности L2 выполнен (darwin/arm64, NumCPU=8, fp32, 2 сессии,
      `--iterations 20`): L1 дёшев (p50 ≈ 0.04–0.08 мс на 53–98 рун, ≈ 7.7 мс на
      8000 рун); L1+L2 на коротком вводе p50 ≈ 50–67 мс / p95 ≈ 80–99 мс; цена окна
      плоская (окно 1024 руны ≈ 275 токенов — ≈ 0.25 с p50, окно у потолка модели —
      ≈ 0.37 с, пик RSS ≈ 4.5 GB), поэтому полная цена длинного ввода — N окон: 8000
      рун (≈ 9 окон) ≈ 2.6 с (D60). Токенайзер mDeBERTa — ≈ 0.27 токена на руну
      русского текста, поэтому дефолтное окно в потолок помещается
      (`TestWindowFitsTokenBudget`).
- [x] SLO guard L2 (пересмотрен, `D53`) достигнут на референсном хосте (8 CPU,
      fp32, 2 сессии): короткий ввод p50 ≈ 50–67 мс / p95 ≈ 80–99 мс, окно у потолка
      модели — p50 ≈ 0.37 с (длинный вход — N окон, D60). Исходная цель `A2` (`p50 ≤ 150 мс` /
      `p95 ≤ 400 мс` на потолке) недостижима и заменена. Ручки: больше
      сессий/потоков, int8, меньшая guard-модель, меньший `MaxTokens`.
- [x] Богатый экспорт L2 — opt-in (`export.py --schema full`): Go принимает
      полную таксономию (32 сущности + 5 групп = 70 меток); артефакт генерируется
      локально, дефолтный экспорт остаётся компактным (D35).
- [x] CLI-ридер трейсов `demo/cmd/traces` (список + `-trace <id>` с observations и scores); JSON-API не реализован.
- [x] `usage.cost`/`cost` заполняются библиотекой по `Config.Pricing` (`model.Pricing`); нулевая цена даёт `0`.
- [x] Guard: деградация по рельсам — `Config.GuardFailOpen`, деградация видна в observation (`guard_degraded`) (I-16, D73).
- [x] Право `chat` на операцию `Chat` (`authz.PermChat`).
- [x] Идентичность эмбеддера: `embed_model` в чанке, поиск фильтрует по текущей модели (BR-79).
- [x] Память: история треда, семантический recall и working-память в ES; право `memory`; скоуп `tenant` + `user_id`; порядок по `seq`; `Chat.ThreadID` (I-17, BR-81…BR-84).
- [x] Spotlighting: результат инструмента оборачивается как недоверенный, напоминание в системном промпте, `Config.Spotlight` (I-18, BR-85).
- [x] Rate limit на тенанта (`Config.RateLimit`) до провайдера/ES/guard; `ErrRateLimited` (BR-86).
- [x] Атомарный ingest: `doc_version` + «сначала запись, потом удаление» (BR-87).
- [x] Кэш ответов без tools (`Config.ResponseCache`), `metadata.cache` (BR-88).
- [x] Стриминг: `ChatStream` со скользящим окном рельса `output`, `model.Streamer`, откат на буферизованный `Chat` (I-19, I-20, BR-89, BR-90).
- [x] OTLP-синк трейсов (`trace.NewOTLP`) без внешних зависимостей (stdlib).
- [x] Часть 3: `flow` сужен до `Step` (D47); `App.Health()` и `HealthChecker` (D48); реранкинг (`rerank`, `SearchRequest.Rerank`, D49); поиск по инструментам (`Config.ToolSearch`, D50); MCP-клиент и `kit.RawTool` (D51); роли документации разведены (D52).
- [x] MCP-сервер и `App.CallTool` (D54): `mcp.NewServer` (stdio и streamable HTTP, `Origin`-allowlist), `mcp.FromTool` ведёт вызов через `App.CallTool`, поэтому право/guard/egress/трейс не пропускаются.
- [x] Возврат PII инструменту (`AllowPII`, `D55`): подстановка только при однозначном отображении заглушки и только внутри вызова; `Verdict.Substitutions` не покидает процесс через callback'и; формат `[PII:…]` не изменился.
- [x] Фаззинг детерминированного слоя и хелперов (`FuzzCanonicalize`, `FuzzDecodedVariants`, `FuzzCompileFilter`, `FuzzRRF`); фаззер нашёл не-фиксированную каноникализацию — `Canonicalize` теперь NFKC → `fold` → NFKC и санирует невалидный UTF-8 (`D56`).
- [x] Хуки фасада (`D57`): `Config.GuardOnViolation` (вердикт без сырых значений) и `Config.TraceProcessors` (правка/отбрасывание наблюдения до записи).
- [x] Валидация `Generate` (`D58`): ответ сверяется со схемой до декодирования; `Generated.Decoded` отличает успешное декодирование от `Warn`/`Fallback`.
- [x] Перенос taint между ходами (`D59`): `untrusted` хранится в `kit-memory-messages`, восстанавливается на истории и recall; Rule of Two больше не обходится вторым ходом.
- [x] Окно детекции (`D60`): текст в пределах `GuardMaxChars` сканируется перекрывающимися окнами `GuardWindowChars`, поэтому L2 проходит весь текст, а не первые 512 токенов.
- [x] `ValidateOutput` fail-closed (`D61`): off-schema результат уходит модели как error-result, ход помечается недоверенным.
- [x] Анализатор `russian` на текстовых полях (`D62`) с честной пометкой платы для смешанного корпуса.
- [x] Смещения находок в координатах исходного текста (`D63`): `canonicalizeMap`/`remapSpan`; маска накрывает весь сопоставленный диапазон (регрессия — ZWSP-набор в `guard/rules`).
- [x] Слои видят разные потоки (`D64`): `guard.NaturalText`; L1 — канонический текст, L2 — исходный.
- [x] Роль группы классификации (`D65`): информационные задачи (`intent`, `tone`) вердикта не дают; `cls_threshold` читается.
- [x] Кэш вердиктов ключуется инспектируемым текстом (`D66`); замер `verify latency` переведён на окна (ручка `--window`, дефолты 20/2).
- [x] Документы приведены в соответствие с кодом (docs/**, README, AGENTS.md,
      demo/README).

## Смотрите также

- [Решения и допущения](./decisions.md) — `D1`…`D79`, `A1`…`A9`
- [Архитектура](./architecture.md) — пакеты и рантайм, на которые опирается
  план
- [Бизнес-правила](./business-rules.md) — `BR-01`…`BR-108`, проверяемые на
  этапах
- [Ориентиры](./references.md) — идеи для реализации
