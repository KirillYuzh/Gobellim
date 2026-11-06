# Gobellim notebooks

Запускаемые «ноутбуки»: каждый сценарий — отдельный `main.go`, который печатает
текстовые ячейки и результат. Jupyter не нужен; `go run` запускает ноутбук
целиком, сверху вниз.

```sh
go run ./examples/notebooks/01_task_mock
```

## Что нужно один раз

### GigaChat free tier

1. Откройте GigaChat Studio и выпустите **Authorization key**.
2. Поставьте его в окружение:

   ```sh
   export GIGACHAT_AUTH_KEY='<Authorization key>'
   ```

   Free tier использует scope `GIGACHAT_API_PERS` (по умолчанию в адаптере).

3. Если запрос падает на TLS из-за российского корневого сертификата, для
   локального демо можно явно отключить проверку:

   ```sh
   export GIGACHAT_INSECURE_TLS=1
   ```

   Это **только для демо**. В сервисе установите доверенный корневой CA.

### Langfuse (опционально)

```sh
export LANGFUSE_PUBLIC_KEY='pk-lf-...'
export LANGFUSE_SECRET_KEY='sk-lf-...'
export LANGFUSE_HOST='https://cloud.langfuse.com'
```

Без ключей sink работает в `DryRun`: он ничего не отправляет, но показывает,
какой JSON ушёл бы в Langfuse (ноутбук 04).

## Сценарии

| Ноутбук                 | Что показывает                                                                | Сеть     |
| ----------------------- | ----------------------------------------------------------------------------- | -------- |
| `01_task_mock`          | один use-case: typed input/output, PII → handles, write → approval card       | нет      |
| `02_gate_mock`          | unknown tool, raw ID, handle, empty-args write, чужой tenant                  | нет      |
| `03_flow_mock`          | линейный flow, approval card, Four-Eyes, replay                               | нет      |
| `04_langfuse_mock`      | Langfuse sink в dry-run: trace / generation / span                            | нет      |
| `05_gigachat_task`      | реальный GigaChat free tier: вопрос → JSON-ответ, audit                       | GigaChat |
| `06_gigachat_approval`  | GigaChat предлагает refund, Gate эскалирует, tool не выполняется              | GigaChat |
| `07_task_flow_approval` | Task эскалирует внутри flow: Suspension, одобрение, `ExecuteApproved`, replay | нет      |

## Как читать вывод

- `─── ... ───` — текстовая ячейка с объяснением.
- `→ ...` — результат выполнения ячейки.
- `↳ tool Run executed` — приложение реально вошло в tool handler.

## Важно

- Мок-ноутбуки детерминированы: `testkit.FakeModel` проигрывает сценарий, а
  `testkit.AllowAll` заменяет RBAC. Это сделано, чтобы показать именно Gate и
  provenance, а не поведение конкретной модели.
- В реальных ноутбуках используется `plugins/gigachat`. GigaChat не гарантирует
  JSON schema, поэтому адаптер добавляет инструкцию к system prompt, снимает
  markdown-обёртку, а core всё равно строго декодирует и валидирует ответ.
- `plugins/langfuse` отправляет только то, что уже есть в audit event: labels,
  digest, decision, latency и токены. Prompt text и сырые аргументы не уходят.
