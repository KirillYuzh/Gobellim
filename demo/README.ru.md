# demo

**[Русский](README.ru.md) · [English](README.md)**

Чат-бот с авторизацией на основе API GigaChat, на скелете Clean Architecture.

Он показывает, для чего нужна библиотека, сквозным сценарием:

- один проверяющий права путь вызова для чата, поиска и инструментов;
- инструмент, недоступный вызывающему (вырезается из схемы до модели);
- база знаний за ACL вызывающего;
- типизированный структурированный вывод (`POST /bot/triage`), а не свободный текст;
- guardrails на входе и выходе;
- второе хранилище, которое принадлежит демо (mock-адаптер аудита), а kit его не трогает.

У бота два инструмента:

- `kb_search`: поиск по базе знаний (alice, root)
- `get_weather`: текущая погода в городе (root)

## Что он отвечает сразу

База знаний заполняется при старте, поэтому эти вопросы работают сразу после
`docker compose up -d`:

| Вопрос                                                  | Ответ                                |
| ------------------------------------------------------- | ------------------------------------ |
| «Сколько дней даётся на возврат товара?»                | 14 календарных дней                  |
| «Сколько идёт доставка в другой город и сколько стоит?» | 2–5 рабочих дней, 400 ₽              |
| «Какова гарантия на электронику?»                       | 12 месяцев                           |
| «Можно ли купить в рассрочку?»                          | да, «0-0-6» от 3000 ₽                |
| «Как связаться с поддержкой?»                           | 8 999 888-77-66, круглосуточно       |
| «Как получить 5% кэшбэка?»                              | от 50 000 ₽                          |
| «Какая погода в Москве?»                                | реальная погода — только для `admin` |

## Слои

```
cmd/bot            корень композиции: конфигурация, адаптеры, HTTP-сервер
cmd/tui            терминальный клиент для бота
internal/
  domain           сущности, ошибки, principal в контексте — не импортирует ничего
  usecase          логика приложения (Auth, Bot, Knowledge) и её порты
  config           .env + переменные окружения
  seed             база знаний, поставляемая с демо
  adapter/
    httpapi        транспорт: маршруты, авторизация bearer, ошибки домена → HTTP
    kitengine      чат, база знаний и два инструмента поверх kit
    weather        провайдер за инструментом get_weather
    audit          mock-заглушка второго хранилища (аналитика) демо
    memory         заглушка Directory и выдача bearer-токенов
    elastic        клиент Elasticsearch, который получает kit
postman            коллекция и окружение для всех эндпоинтов
policy.yaml        RBAC: user → база знаний, admin → база знаний + погода
```

## Запуск

```sh
cd demo/deploy && docker compose up -d
```

Так поднимаются Elasticsearch, сервер эмбеддингов
([text-embeddings-inference](https://github.com/huggingface/text-embeddings-inference)
с `intfloat/multilingual-e5-small`), Grafana и бот. Бот заполняет базу знаний при
старте, пропускает уже существующие документы и пишет в лог `knowledge base
ready`.

Чтобы запустить бота из исходников, поднимите только зависимости:

```sh
docker compose up -d elasticsearch grafana embeddings

cp demo/.env.example demo/.env # добавить GIGACHAT_CREDENTIALS
go -C demo run ./cmd/bot
```

Сервис слушает `:8080` (`KIT_ADDR`) и после готовности пишет в лог
`bot listening`.

По умолчанию бот обращается к серверу `embeddings`. Со сборкой `-tags ort` и
`EMBED_LOCAL=true` он вместо сервера поднимает локальный ONNX-энкодер и читает
веса из `<KIT_MODELS_DIR>/<org>__<name>/` (сначала скачайте их:
`go -C cmd run ./kitctl model pull intfloat/multilingual-e5-small`); тогда
`EMBED_MODEL` задаёт локальную модель (её размерность берётся из графа), а сервис
`embeddings` больше не нужен.

```sh
EMBED_LOCAL=true CGO_LDFLAGS="-L.ort/lib" go -C demo run -tags ort ./cmd/bot
```

## Эндпоинты

| Метод    | Путь          | Авторизация | Тело                          | Ответ                                             |
| -------- | ------------- | ----------- | ----------------------------- | ------------------------------------------------- |
| `GET`    | `/healthz`    | —           | —                             | `{"status":"ok"}`                                 |
| `POST`   | `/auth/login` | —           | `{"login":"alice"}`           | `{"token","user_id","login","role","expires_at"}` |
| `GET`    | `/auth/me`    | bearer      | —                             | `{"user_id","login","role","tenant"}`             |
| `POST`   | `/bot/ask`    | bearer      | `{"prompt":"…"}`              | `{"answer":"…"}`                                  |
| `POST`   | `/bot/triage` | bearer      | `{"text":"…"}`                | `{"severity":1..5,"summary":"…"}`                 |
| `GET`    | `/stats`      | admin       | —                             | `{"Events","ByRole","ByAction"}`                  |
| `GET`    | `/kb`         | admin       | —                             | `{"documents":[{"id","title","chunks"}]}`         |
| `POST`   | `/kb`         | admin       | `{"id","title","text","acl"}` | `{"id","title"}`                                  |
| `GET`    | `/kb/{id}`    | admin       | —                             | `{"id","title","text","acl","chunks"}`            |
| `PUT`    | `/kb/{id}`    | admin       | `{"title","text","acl"}`      | `{"id","title"}`                                  |
| `DELETE` | `/kb/{id}`    | admin       | —                             | `204`                                             |
| `POST`   | `/kb/search`  | bearer      | `{"query":"…","top_k":3}`     | `{"hits":[{"doc_id","title","text","position"}]}` |

```sh
TOKEN=$(curl -s localhost:8080/auth/login -d '{"login":"root"}' | sed 's/.*"token":"\([^"]*\)".*/\1/')

curl -s localhost:8080/auth/me -H "Authorization: Bearer $TOKEN"

curl -s localhost:8080/bot/ask -H "Authorization: Bearer $TOKEN" \
  -d '{"prompt":"Сколько дней даётся на возврат товара?"}'

curl -s localhost:8080/bot/ask -H "Authorization: Bearer $TOKEN" \
  -d '{"prompt":"Какая погода в Москве?"}'

curl -s localhost:8080/bot/ask -H "Authorization: Bearer $TOKEN" \
  -d '{"prompt":"Расскажи анекдот"}'      # без инструмента

# Типизированный вывод: kit выводит схему из domain.Ticket и проверяет ответ.
curl -s localhost:8080/bot/triage -H "Authorization: Bearer $TOKEN" \
  -d '{"text":"не проходит оплата"}'

# Второе хранилище демо, а не kit.
curl -s localhost:8080/stats -H "Authorization: Bearer $TOKEN"
```

`alice` получит тот же ответ про возврат, но на вопрос о погоде ответит отказом —
инструмента нет в её схеме.

## Guardrails

Вход и выход модели проходят два слоя проверок.

- **L1** — детерминированные правила: prompt injection отклоняется, российские
  идентификаторы (СНИЛС, ИНН) маскируются.
- **L2** — `hivetrace/gliner-guard-omni` в ONNX, без Python в рантайме: находит
  ПДн в тексте и классифицирует сообщение как `safe` или `unsafe`.

ПДн маскируются (`[PII:email]`, `[PII:person]`), сообщение, помеченное `unsafe`,
отклоняется с `400`.

L2 доступен только в сборке с тегом `ort`:

```sh
CGO_LDFLAGS="-L.ort/lib" go -C demo run -tags ort ./cmd/bot
```

Без тега бот стартует с одним L1 и пишет в лог `guard L2 not enabled`. Веса лежат
в `models/hivetrace__gliner-guard-omni` и в git не хранятся; как их собрать — в
[tools/gliner](../tools/gliner/README.md).

## TUI

```sh
go -C demo run ./cmd/tui
```

Входит как `alice` и остаётся открытым. Любая строка, не начинающаяся с `/`,
уходит боту.

| Команда                    | Действие                                  |
| -------------------------- | ----------------------------------------- |
| `<текст>`                  | спросить бота                             |
| `/login <user>`            | сменить пользователя (`alice` или `root`) |
| `/me`                      | показать текущего пользователя            |
| `/kb <запрос>`             | поиск по базе знаний напрямую             |
| `/clear`                   | очистить экран                            |
| `/quit`, `/exit`, `Ctrl+D` | выйти                                     |

Флаги: `-url` (или `KIT_BOT_URL`), `-timeout`. `NO_COLOR` отключает цвета.

## Трейсы

Чтение трейсов, observations и оценок прямо из Elasticsearch:

```sh
go -C demo run ./cmd/traces                      # 20 последних трейсов
go -C demo run ./cmd/traces -name chat -tenant bot
go -C demo run ./cmd/traces -trace <trace-id>    # один трейс целиком
```

Флаги: `-url` (или `KIT_ES_URL`), `-limit`, `-name`, `-tenant`, `-trace`.

## Postman

```sh
npx --yes newman run demo/postman/kit-demo.postman_collection.json \
  -e demo/postman/kit-demo.postman_environment.json
```

## Конфигурация

| Переменная             | По умолчанию                     | Назначение                                                |
| ---------------------- | -------------------------------- | --------------------------------------------------------- |
| `KIT_ADDR`             | `:8080`                          | адрес прослушивания                                       |
| `KIT_ES_URL`           | `http://localhost:9200`          | Elasticsearch                                             |
| `EMBED_BASE_URL`       | `http://localhost:8088/v1`       | OpenAI-совместимый сервер эмбеддингов                     |
| `EMBED_MODEL`          | `intfloat/multilingual-e5-small` | модель эмбеддингов                                        |
| `EMBED_DIM`            | `384`                            | размер вектора; должен совпадать с моделью                |
| `EMBED_LOCAL`          | `false`                          | локальный ONNX-энкодер вместо сервера (нужен `-tags ort`) |
| `KIT_MODELS_DIR`       | `models`                         | каталог с весами моделей (только `-tags ort`)             |
| `GLINER_MODEL`         | `hivetrace__gliner-guard-omni`   | подкаталог с guard-моделью                                |
| `SEED_KB`              | `true`                           | заполнять базу знаний при старте                          |
| `WEATHER_BASE_URL`     | `https://wttr.in`                | базовый URL провайдера погоды                             |
| `KIT_POLICY`           | `demo/policy.yaml`               | политика RBAC                                             |
| `TRACE_TEXT`           | `masked`                         | политика текста трейса: `masked` или `full`               |
| `TRACE_SAMPLE`         | `always`                         | сэмплирование трейсов: `always`, `never`, `ratio:0.1`     |
| `GIGACHAT_CREDENTIALS` | —                                | base64 `clientID:clientSecret` (обязательно)              |
| `GIGACHAT_MODEL`       | `GigaChat-2`                     | имя модели                                                |
| `GIGACHAT_BASE_URL`    | значение Сбера                   | базовый URL GigaChat                                      |
| `GIGACHAT_AUTH_URL`    | значение Сбера                   | URL OAuth GigaChat                                        |
| `GIGACHAT_CA_FILE`     | —                                | дополнительный корневой CA                                |
| `KIT_HTTP_TIMEOUT`     | `60s`                            | таймаут HTTP к провайдерам                                |
| `KIT_TOKEN_TTL`        | `12h`                            | время жизни токена-заглушки                               |
