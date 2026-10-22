# Обзор

MCP (Model Context Protocol)

`mcp` — клиент Model Context Protocol (stdio и streamable HTTP). `Register`
экспонирует инструменты как `mcp:<server>:<tool>`.

Инструменты MCP — это egress-инструменты: аргументы уходят к MCP-серверу.
Allowlist egress выводится из адреса endpoint'а (`gobellim.Egress(<host>)`), так
что доступен только этот хост. У stdio-клиента адреса нет — вызывающий обязан
передать политику сам (`gobellim.Egress("example.com")`); дефолта, открывающего
egress на любой хост, нет. Свои опции можно передать в `Register`, они
применяются после дефолта и могут сузить allowlist или потребовать подтверждения.

## Устройство

Как это устроено → [implementation.md](./implementation.md)

## Смотрите также

- [Быстрый старт](../overview.md)
- [Инструменты](../tools/README.md) — MCP-инструменты как обычные gobellim.Tool
