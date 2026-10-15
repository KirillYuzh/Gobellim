# demo

**[Русский](README.ru.md) · [English](README.md)**

Чат-бот с авторизацией на основе API GigaChat.

У бота два инструмента: `get_weather` (текущая погода в городе) и `get_user_role` (роль пользователя).

## Слои

```
cmd/bot
internal/
  domain
  usecase
  config
  adapter/
    httpapi
    kitengine      порт движка чата (+ два инструмента)
    weather        провайдер инструмента погоды
    memory         mock Directory и выдача bearer-токенов
    elastic        клиент Elasticsearch
```

## Запуск

```sh
cd deploy && docker compose up -d elasticsearch grafana
```

```sh
go run ./demo/cmd/bot
```

Сервис слушает `:8080` (`KIT_ADDR`) и после готовности пишет в лог
`bot listening`.

## Эндпоинты

| Метод  | Путь          | Auth   | Тело                | Ответ                                             |
| ------ | ------------- | ------ | ------------------- | ------------------------------------------------- |
| `GET`  | `/healthz`    | —      | —                   | `{"status":"ok"}`                                 |
| `POST` | `/auth/login` | —      | `{"login":"alice"}` | `{"token","user_id","login","role","expires_at"}` |
| `GET`  | `/auth/me`    | bearer | —                   | `{"user_id","login","role","tenant"}`             |
| `POST` | `/bot/ask`    | bearer | `{"prompt":"…"}`    | `{"answer":"…"}`                                  |

Mock-пользователи: `alice` (роль `user`, только инструмент погоды) и `root`
(роль `admin`, оба инструмента).

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
  -d '{"prompt":"Расскажи анекдот"}'      # без инструмента
```

## Конфигурация

| Переменная             | По умолчанию            | Назначение                     |
| ---------------------- | ----------------------- | ------------------------------ |
| `KIT_ADDR`             | `:8080`                 | адрес прослушивания            |
| `KIT_ES_URL`           | `http://localhost:9200` | Elasticsearch                  |
| `KIT_POLICY`           | `demo/policy.yaml`      | политика RBAC                  |
| `WEATHER_BASE_URL`     | `https://wttr.in`       | базовый URL провайдера погоды  |
| `GIGACHAT_CREDENTIALS` | —                       | base64 `clientID:clientSecret` |
| `GIGACHAT_MODEL`       | `GigaChat-2`            | имя модели                     |
| `GIGACHAT_BASE_URL`    | `https://api.giga.chat` | базовый URL GigaChat           |
| `GIGACHAT_AUTH_URL`    | Sber OAuth endpoint     | URL OAuth GigaChat             |
| `GIGACHAT_CA_FILE`     | —                       | дополнительный корневой CA     |
| `KIT_HTTP_TIMEOUT`     | `60s`                   | таймаут HTTP к провайдерам     |
| `KIT_TOKEN_TTL`        | `12h`                   | время жизни mock-токена        |
