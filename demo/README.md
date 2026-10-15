# demo

**[English](README.md) · [Русский](README.ru.md)**

An authorized chat-bot built on the GigaChat API.

The bot has two tools: `get_weather` (current weather for a city) and `get_user_role` (the user's role).

## Layers

```
cmd/bot
internal/
  domain
  usecase
  config
  adapter/
    httpapi
    kitengine      the chat-engine port (+ the two tools)
    weather        the provider behind the weather tool
    memory         mock Directory and bearer-token issuer
    elastic        Elasticsearch client
```

## Run

```sh
cd deploy && docker compose up -d elasticsearch grafana
```

```sh
go run ./demo/cmd/bot
```

The service listens on `:8080` (`KIT_ADDR`) and logs `bot listening` once it is
ready.

## Endpoints

| Method | Path          | Auth   | Body                | Response                                          |
| ------ | ------------- | ------ | ------------------- | ------------------------------------------------- |
| `GET`  | `/healthz`    | —      | —                   | `{"status":"ok"}`                                 |
| `POST` | `/auth/login` | —      | `{"login":"alice"}` | `{"token","user_id","login","role","expires_at"}` |
| `GET`  | `/auth/me`    | bearer | —                   | `{"user_id","login","role","tenant"}`             |
| `POST` | `/bot/ask`    | bearer | `{"prompt":"…"}`    | `{"answer":"…"}`                                  |

Mock users: `alice` (role `user`, weather tool only) and `root` (role `admin`,
both tools).

```sh
TOKEN=$(curl -s localhost:8080/auth/login -d '{"login":"root"}' | sed 's/.*"token":"\([^"]*\)".*/\1/')
```

```sh
curl -s localhost:8080/auth/me -H "Authorization: Bearer $TOKEN"
```

```sh
curl -s localhost:8080/bot/ask -H "Authorization: Bearer $TOKEN" \
  -d '{"prompt":"Какая погода в Москве?"}'
```

```sh
curl -s localhost:8080/bot/ask -H "Authorization: Bearer $TOKEN" \
  -d '{"prompt":"Какая у меня роль в системе?"}'
```

```sh
curl -s localhost:8080/bot/ask -H "Authorization: Bearer $TOKEN" \
  -d '{"prompt":"Расскажи анекдот"}'      # no tool needed
```

## Configuration

| Variable               | Default                 | Meaning                        |
| ---------------------- | ----------------------- | ------------------------------ |
| `KIT_ADDR`             | `:8080`                 | listen address                 |
| `KIT_ES_URL`           | `http://localhost:9200` | Elasticsearch                  |
| `KIT_POLICY`           | `demo/policy.yaml`      | RBAC policy                    |
| `WEATHER_BASE_URL`     | `https://wttr.in`       | weather provider base URL      |
| `GIGACHAT_CREDENTIALS` | —                       | base64 `clientID:clientSecret` |
| `GIGACHAT_MODEL`       | `GigaChat-2`            | model name                     |
| `GIGACHAT_BASE_URL`    | `https://api.giga.chat` | GigaChat base URL              |
| `GIGACHAT_AUTH_URL`    | Sber OAuth endpoint     | GigaChat OAuth URL             |
| `GIGACHAT_CA_FILE`     | —                       | extra root CA                  |
| `KIT_HTTP_TIMEOUT`     | `60s`                   | provider HTTP timeout          |
| `KIT_TOKEN_TTL`        | `12h`                   | mock token lifetime            |
