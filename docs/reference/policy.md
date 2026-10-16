# policy.yaml

`policy.yaml` — единственный источник прав. Это RBAC с deny-by-default: роль получает только перечисленные права, всё остальное запрещено. Файл разбирает `authz`, а `kit.New` принимает готовый `*authz.Policy`.

## Формат

| Ключ                       | Значение     | Правило                                                                                                       |
| -------------------------- | ------------ | ------------------------------------------------------------------------------------------------------------- |
| `version`                  | `1`          | Другое значение → `ErrInvalidConfig`.                                                                         |
| `default`                  | `deny`       | Допустимо только `deny`; иначе `ErrInvalidConfig`.                                                            |
| `roles`                    | отображение  | Имя роли → набор `permissions`.                                                                               |
| `roles.<name>.permissions` | список строк | Права роли: `chat`, `memory`, `search`, `ingest`, `delete`, `trace`, `generate`, `prune`, `tool:<name>`, `*`. |

Полный пример:

```yaml
version: 1
default: deny

roles:
  user:
    permissions:
      - chat
      - tool:get_weather

  admin:
    permissions:
      - "*"
```

| Роль    | Что разрешено                       |
| ------- | ----------------------------------- |
| `user`  | `chat`, `tool:get_weather`          |
| `admin` | всё (`*` — wildcard на любое право) |

## Права

Права объявлены константами в `authz`; рантайм проверяет их через `authz.Policy.Authorize(principal, permission)`.

| Константа              | Значение      | Где проверяется                                                               |
| ---------------------- | ------------- | ----------------------------------------------------------------------------- |
| `authz.PermChat`       | `chat`        | `App.Chat`: вызов модели и весь цикл инструментов.                            |
| `authz.PermMemory`     | `memory`      | Операции памяти и `App.Chat` с `ThreadID`.                                    |
| `authz.PermSearch`     | `search`      | `App.Search`: и вызов эмбеддера `Query()`, и `_msearch`.                      |
| `authz.PermIngest`     | `ingest`      | `App.Ingest`: вызов эмбеддера `Passages()` и запись чанков.                   |
| `authz.PermDelete`     | `delete`      | `App.Delete`.                                                                 |
| `authz.PermTrace`      | `trace`       | `App.Score`.                                                                  |
| `authz.PermGenerate`   | `generate`    | `Generate[T]`.                                                                |
| `authz.PermPrune`      | `prune`       | `App.Prune`.                                                                  |
| `authz.ToolPermission` | `tool:<name>` | Исполнение инструмента и вычитание запрещённых из схемы (`allowedToolSpecs`). |

`"*"` (`wildcard`) в списке прав роли разрешает любое право. Пустое право (`""`) проверку пропускает: так проходят внутренние операции без собственного публичного права. `App.Chat` требует права `chat`. Права ролей объединяются: достаточно совпадения в любой из ролей принципала.

`Ingest`, `Search` и `Delete` дополнительно требуют непустой `Tenant` принципала: без него операция возвращает `ErrUnauthorized` (иначе поиск и запись расширились бы на все тенанты с той же ролью). `Prune` проверяет только принципала и право `prune`, без тената (`design.md` §5.6): это глобальное действие эксплуатации по времени, как ILM.

Отказ по праву несёт `ErrUnauthorized`; ошибка разбора политики — `ErrInvalidConfig`.

## Строгий режим

`authz.Parse` использует `yaml.Decoder` с `KnownFields(true)`: неизвестное поле отклоняется. Ошибка разбора, неизвестное поле, неподдерживаемая версия и неверный `default` несут `ErrInvalidConfig`; `authz.Load` открывает файл и передаёт его в `authz.Parse`. Если в `kit.Config` передан `Policy: nil`, `kit.New` возвращает `ErrInvalidConfig`.

Проверка файла без запуска сервиса:

```sh
kitctl policy check ./policy.yaml
```

## Смотрите также

- [Справочник kit](./kit.md) — `kit.New` и поле `Policy`
- [Доступ](../concepts/access-control.md) — принципал, роли и ACL документов
- [Ошибки](./errors.md) — `ErrUnauthorized` и `ErrInvalidConfig`
- [Бизнес-правила](../internals/business-rules.md) — `BR-08`, `BR-14`
