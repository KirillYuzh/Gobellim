# demo

**[English](README.md) · [Русский](README.ru.md)**

An authorized bot built on the GigaChat API.

The bot has two tools:

- `kb_search`: search over the knowledge base (user, admin)
- `get_weather`: current weather in a city (admin)

## What it answers out of the box

The knowledge base is seeded on start, so these work right after
`docker compose up -d`:

| Ask                                                     | Answer                         |
| ------------------------------------------------------- | ------------------------------ |
| «Сколько дней даётся на возврат товара?»                | 14 календарных дней            |
| «Сколько идёт доставка в другой город и сколько стоит?» | 2–5 рабочих дней, 400 ₽        |
| «Какова гарантия на электронику?»                       | 12 месяцев                     |
| «Можно ли купить в рассрочку?»                          | да, «0-0-6» от 3000 ₽          |
| «Как связаться с поддержкой?»                           | 8 800 555-35-35, круглосуточно |
| «Как получить 5% кэшбэка?»                              | от 50 000 ₽                    |
| «Какая погода в Москве?»                                | real weather — `admin` only    |

## Layers

```
cmd/bot            composition root: config, adapters, HTTP server
cmd/tui            terminal client for the bot
internal/
  domain           entities, errors, principal in context — imports nothing
  usecase          application logic (Auth, Bot, Knowledge) and its ports
  config           .env + environment variables
  seed             the knowledge base shipped with the demo
  adapter/
    httpapi        transport: routing, bearer auth, domain errors → HTTP
    kitengine      chat, the knowledge base and the two tools on top of kit
    weather        the provider behind the get_weather tool
    memory         mock Directory and bearer-token issuance
    elastic        the Elasticsearch client that kit receives
postman            collection and environment for every endpoint
policy.yaml        RBAC: user → knowledge base, admin → knowledge base + weather
```

## Run

```sh
cd deploy && docker compose up -d
```

That starts Elasticsearch, the embeddings server
([text-embeddings-inference](https://github.com/huggingface/text-embeddings-inference)
with `intfloat/multilingual-e5-small`), Grafana and the bot. The bot seeds the
knowledge base on start, skips documents that already exist, and logs
`knowledge base ready`.

To run the bot from source, start only the dependencies:

```sh
docker compose up -d elasticsearch grafana embeddings

cp demo/.env.example demo/.env # add GIGACHAT_CREDENTIALS
go run ./demo/cmd/bot
```

The service listens on `:8080` (`KIT_ADDR`) and logs `bot listening` once it is
ready.

## Endpoints

| Method   | Path          | Auth   | Body                          | Response                                          |
| -------- | ------------- | ------ | ----------------------------- | ------------------------------------------------- |
| `GET`    | `/healthz`    | —      | —                             | `{"status":"ok"}`                                 |
| `POST`   | `/auth/login` | —      | `{"login":"alice"}`           | `{"token","user_id","login","role","expires_at"}` |
| `GET`    | `/auth/me`    | bearer | —                             | `{"user_id","login","role","tenant"}`             |
| `POST`   | `/bot/ask`    | bearer | `{"prompt":"…"}`              | `{"answer":"…"}`                                  |
| `GET`    | `/kb`         | admin  | —                             | `{"documents":[{"id","title","chunks"}]}`         |
| `POST`   | `/kb`         | admin  | `{"id","title","text","acl"}` | `{"id","title"}`                                  |
| `GET`    | `/kb/{id}`    | admin  | —                             | `{"id","title","text","acl","chunks"}`            |
| `PUT`    | `/kb/{id}`    | admin  | `{"title","text","acl"}`      | `{"id","title"}`                                  |
| `DELETE` | `/kb/{id}`    | admin  | —                             | `{"id","title"}`                                  |
| `POST`   | `/kb/search`  | bearer | `{"query":"…","top_k":3}`     | `{"hits":[{"doc_id","title","text","position"}]}` |

```sh
TOKEN=$(curl -s localhost:8080/auth/login -d '{"login":"root"}' | sed 's/.*"token":"\([^"]*\)".*/\1/')

curl -s localhost:8080/auth/me -H "Authorization: Bearer $TOKEN"

curl -s localhost:8080/bot/ask -H "Authorization: Bearer $TOKEN" \
  -d '{"prompt":"Сколько дней даётся на возврат товара?"}'

curl -s localhost:8080/bot/ask -H "Authorization: Bearer $TOKEN" \
  -d '{"prompt":"Какая погода в Москве?"}'

curl -s localhost:8080/bot/ask -H "Authorization: Bearer $TOKEN" \
  -d '{"prompt":"Расскажи анекдот"}'      # no tool needed
```

`alice` gets the same answer about returns, but answers the weather question with
a refusal — the tool is not in her schema.

## Guardrails

The model's input and output pass two layers of checks.

- **L1** — deterministic rules: prompt injection is rejected, Russian personal
  IDs (SNILS, INN) are masked.
- **L2** — `hivetrace/gliner-guard-omni` in ONNX, with no Python at runtime: it
  finds PII in the text and classifies the message as `safe` or `unsafe`.

PII is masked (`[PII:email]`, `[PII:person]`) and a message marked `unsafe` is
rejected with `400`.

L2 is available only in a build with the `ort` tag:

```sh
CGO_LDFLAGS="-L.ort/lib" go run -tags ort ./demo/cmd/bot
```

Without the tag the bot starts with L1 alone and logs `guard L2 not enabled`.
The weights live in `models/hivetrace__gliner-guard-omni` and are not kept in
git; see [tools/gliner](../tools/gliner/README.md) for how to build them.

## TUI

```sh
go run ./demo/cmd/tui
```

Logs in as `alice` and stays open. Any line that does not start with `/` goes to
the bot.

| Command                    | Effect                             |
| -------------------------- | ---------------------------------- |
| `<text>`                   | ask the bot                        |
| `/login <user>`            | switch user (`alice` or `root`)    |
| `/me`                      | print the current principal        |
| `/kb <query>`              | search the knowledge base directly |
| `/clear`                   | clear the screen                   |
| `/quit`, `/exit`, `Ctrl+D` | leave                              |

Flags: `-url` (or `KIT_BOT_URL`), `-timeout`. `NO_COLOR` disables the colors.

## Postman

```sh
npx --yes newman run demo/postman/kit-demo.postman_collection.json \
  -e demo/postman/kit-demo.postman_environment.json
```

## Configuration

| Variable               | Default                          | Meaning                                      |
| ---------------------- | -------------------------------- | -------------------------------------------- |
| `KIT_ADDR`             | `:8080`                          | listen address                               |
| `KIT_ES_URL`           | `http://localhost:9200`          | Elasticsearch                                |
| `EMBED_BASE_URL`       | `http://localhost:8088/v1`       | OpenAI-compatible embeddings server          |
| `EMBED_MODEL`          | `intfloat/multilingual-e5-small` | embedding model                              |
| `EMBED_DIM`            | `384`                            | vector size; must match the model            |
| `KIT_MODELS_DIR`       | `models`                         | root of the model weights (`-tags ort` only) |
| `GLINER_MODEL`         | `hivetrace__gliner-guard-omni`   | subdirectory holding the guard model         |
| `SEED_KB`              | `true`                           | seed the knowledge base on start             |
| `WEATHER_BASE_URL`     | `https://wttr.in`                | weather provider base URL                    |
| `KIT_POLICY`           | `demo/policy.yaml`               | RBAC policy                                  |
| `GIGACHAT_CREDENTIALS` | —                                | base64 `clientID:clientSecret` (required)    |
| `GIGACHAT_MODEL`       | `GigaChat-2`                     | model name                                   |
| `GIGACHAT_BASE_URL`    | Sber default                     | GigaChat base URL                            |
| `GIGACHAT_AUTH_URL`    | Sber default                     | GigaChat OAuth URL                           |
| `GIGACHAT_CA_FILE`     | —                                | extra root CA                                |
| `KIT_HTTP_TIMEOUT`     | `60s`                            | provider HTTP timeout                        |
| `KIT_TOKEN_TTL`        | `12h`                            | mock token lifetime                          |
