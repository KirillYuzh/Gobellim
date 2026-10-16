# demo

**[English](README.md) · [Русский](README.ru.md)**

An authorized bot built on the GigaChat API, on a Clean Architecture skeleton.

It shows what the library is for, end to end:

- one permission-checked call path for chat, retrieval and tools;
- a tool the caller may not use (removed from the schema before the model runs);
- a knowledge base behind the caller's ACL;
- typed structured output (`POST /bot/triage`), not free text;
- guardrails on input and output;
- a second store the demo owns (the mock audit adapter) that kit never touches.

The bot has two tools:

- `kb_search`: search over the knowledge base (alice, root)
- `get_weather`: current weather in a city (root)

## What it answers out of the box

The knowledge base is seeded on start, so these work right after
`docker compose up -d`:

| Ask                                            | Answer                   |
| ---------------------------------------------- | ------------------------ |
| "How many days do I have to return an item?"   | 14 calendar days         |
| "How long does delivery to another city take?" | 2–5 business days, ₽400  |
| "What is the warranty on electronics?"         | 12 months                |
| "Can I buy in installments?"                   | yes, "0-0-6" from ₽3,000 |
| "How can I contact support?"                   | 8 999 888-77-66, 24/7    |
| "How do I get 5% cashback?"                    | from ₽50,000             |

The seeded sources are Russian, so the bot may answer in Russian.

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
    kitengine      chat, retrieval and two tools on top of kit
    weather        the provider behind the get_weather tool
    audit          mock second store (analytics) the demo owns
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

By default the bot calls the `embeddings` server. With the `ort` tag and
`EMBED_LOCAL=true` it runs the in-process ONNX embedder instead, reading
`<KIT_MODELS_DIR>/<org>__<name>/` (download the weights first with
`go run ./cmd/kitctl model pull intfloat/multilingual-e5-small`); `EMBED_MODEL`
`EMBED_MODEL` then names the local model (its dimension comes from the graph),
and the `embeddings` service is no longer needed.

```sh
EMBED_LOCAL=true CGO_LDFLAGS="-L.ort/lib" go run -tags ort ./demo/cmd/bot
```

## Endpoints

| Method   | Path          | Auth   | Body                          | Response                                          |
| -------- | ------------- | ------ | ----------------------------- | ------------------------------------------------- |
| `GET`    | `/healthz`    | —      | —                             | `{"status":"ok"}`                                 |
| `POST`   | `/auth/login` | —      | `{"login":"alice"}`           | `{"token","user_id","login","role","expires_at"}` |
| `GET`    | `/auth/me`    | bearer | —                             | `{"user_id","login","role","tenant"}`             |
| `POST`   | `/bot/ask`    | bearer | `{"prompt":"…"}`              | `{"answer":"…"}`                                  |
| `POST`   | `/bot/triage` | bearer | `{"text":"…"}`                | `{"severity":1..5,"summary":"…"}`                 |
| `GET`    | `/stats`      | admin  | —                             | `{"Events","ByRole","ByAction"}`                  |
| `GET`    | `/kb`         | admin  | —                             | `{"documents":[{"id","title","chunks"}]}`         |
| `POST`   | `/kb`         | admin  | `{"id","title","text","acl"}` | `{"id","title"}`                                  |
| `GET`    | `/kb/{id}`    | admin  | —                             | `{"id","title","text","acl","chunks"}`            |
| `PUT`    | `/kb/{id}`    | admin  | `{"title","text","acl"}`      | `{"id","title"}`                                  |
| `DELETE` | `/kb/{id}`    | admin  | —                             | `204`                                             |
| `POST`   | `/kb/search`  | bearer | `{"query":"…","top_k":3}`     | `{"hits":[{"doc_id","title","text","position"}]}` |

```sh
TOKEN=$(curl -s localhost:8080/auth/login -d '{"login":"root"}' | sed 's/.*"token":"\([^"]*\)".*/\1/')

curl -s localhost:8080/auth/me -H "Authorization: Bearer $TOKEN"

curl -s localhost:8080/bot/ask -H "Authorization: Bearer $TOKEN" \
  -d '{"prompt":"How many days do I have to return an item?"}'

curl -s localhost:8080/bot/ask -H "Authorization: Bearer $TOKEN" \
  -d '{"prompt":"Tell me a joke"}'      # no tool needed

# Typed output: kit derives the schema from domain.Ticket and validates the answer.
curl -s localhost:8080/bot/triage -H "Authorization: Bearer $TOKEN" \
  -d '{"text":"the payment does not go through"}'

# The demo's own mock store, not kit's.
curl -s localhost:8080/stats -H "Authorization: Bearer $TOKEN"
```

`alice` gets the same answers; asking her for the weather returns a refusal,
because the tool is not in her schema.

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

## Traces

Read traces, observations and scores straight from Elasticsearch:

```sh
go run ./demo/cmd/traces                      # the 20 latest traces
go run ./demo/cmd/traces -name chat -tenant bot
go run ./demo/cmd/traces -trace <trace-id>    # one trace in full
```

Flags: `-url` (or `KIT_ES_URL`), `-limit`, `-name`, `-tenant`, `-trace`.

## Postman

```sh
npx --yes newman run demo/postman/kit-demo.postman_collection.json \
  -e demo/postman/kit-demo.postman_environment.json
```

## Configuration

| Variable               | Default                          | Meaning                                              |
| ---------------------- | -------------------------------- | ---------------------------------------------------- |
| `KIT_ADDR`             | `:8080`                          | listen address                                       |
| `KIT_ES_URL`           | `http://localhost:9200`          | Elasticsearch                                        |
| `EMBED_BASE_URL`       | `http://localhost:8088/v1`       | OpenAI-compatible embeddings server                  |
| `EMBED_MODEL`          | `intfloat/multilingual-e5-small` | embedding model                                      |
| `EMBED_DIM`            | `384`                            | vector size; must match the model                    |
| `EMBED_LOCAL`          | `false`                          | use the in-process ONNX embedder (needs `-tags ort`) |
| `KIT_MODELS_DIR`       | `models`                         | root of the model weights (`-tags ort` only)         |
| `GLINER_MODEL`         | `hivetrace__gliner-guard-omni`   | subdirectory holding the guard model                 |
| `SEED_KB`              | `true`                           | seed the knowledge base on start                     |
| `WEATHER_BASE_URL`     | `https://wttr.in`                | weather provider base URL                            |
| `KIT_POLICY`           | `demo/policy.yaml`               | RBAC policy                                          |
| `TRACE_TEXT`           | `masked`                         | trace text policy: `masked` or `full`                |
| `TRACE_SAMPLE`         | `always`                         | trace sampling: `always`, `never`, `ratio:0.1`       |
| `GIGACHAT_CREDENTIALS` | —                                | base64 `clientID:clientSecret` (required)            |
| `GIGACHAT_MODEL`       | `GigaChat-2`                     | model name                                           |
| `GIGACHAT_BASE_URL`    | Sber default                     | GigaChat base URL                                    |
| `GIGACHAT_AUTH_URL`    | Sber default                     | GigaChat OAuth URL                                   |
| `GIGACHAT_CA_FILE`     | —                                | extra root CA                                        |
| `KIT_HTTP_TIMEOUT`     | `60s`                            | provider HTTP timeout                                |
| `KIT_TOKEN_TTL`        | `12h`                            | mock token lifetime                                  |
