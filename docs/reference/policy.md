# policy.yaml

`policy.yaml` — единственный источник прав. Это RBAC с deny-by-default: роль получает только перечисленные права, всё остальное запрещено. Файл разбирает `authz`, а `gobellim.New` принимает готовый `*authz.Policy`.

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

| Константа              | Значение      | Где проверяется                                                                                       |
| ---------------------- | ------------- | ----------------------------------------------------------------------------------------------------- |
| `authz.PermChat`       | `chat`        | `App.Chat`: вызов модели и весь цикл инструментов.                                                    |
| `authz.PermMemory`     | `memory`      | Операции памяти и `App.Chat` с `ThreadID`.                                                            |
| `authz.PermSearch`     | `search`      | `App.Search`: и вызов эмбеддера `Query()`, и `_msearch`.                                              |
| `authz.PermIngest`     | `ingest`      | `App.Ingest`: вызов эмбеддера `Passages()` и запись чанков.                                           |
| `authz.PermDelete`     | `delete`      | `App.Delete`.                                                                                         |
| `authz.PermTrace`      | `trace`       | `App.Score`.                                                                                          |
| `authz.PermGenerate`   | `generate`    | `Generate[T]`.                                                                                        |
| `authz.PermPrune`      | `prune`       | `App.Prune`.                                                                                          |
| `authz.ToolPermission` | `tool:<name>` | Исполнение инструмента и вычитание запрещённых из схемы (`allowedToolSpecs`), а также `App.CallTool`. |

`"*"` (`wildcard`) в списке прав роли разрешает любое право. Права ролей объединяются: достаточно совпадения в любой из ролей принципала.

Пустое право (`""`) — это ошибка конфигурации, а не разрешение: `authz.Authorize`
возвращает `ErrInvalidConfig`, а `authz.Allows` — `false`. Забытый аргумент не
превращается в обход RBAC. Каждый инструмент поэтому имеет право по умолчанию:
`tool.New` выставляет `tool:<name>`, если `Perm` не задан, поэтому политика
открывает инструмент по имени, а любой `tool:<name>` без права в политике
запрещён.

`Ingest`, `Search` и `Delete` дополнительно требуют непустой `Tenant` принципала: без него операция возвращает `ErrUnauthorized` (иначе поиск и запись расширились бы на все тенанты с той же ролью). `Prune` проверяет только принципала и право `prune`, без тената (`design.md` §5.6): это глобальное действие эксплуатации по времени, как ILM.

Отказ по праву несёт `ErrUnauthorized`; ошибка разбора политики — `ErrInvalidConfig`.

## Строгий режим

`authz.Parse` использует `yaml.Decoder` с `KnownFields(true)`: неизвестное поле отклоняется. Файл должен содержать ровно один YAML-документ: второй отклоняется с `ErrInvalidConfig`, иначе молча отброшенная часть политики выглядела бы как успешная проверка. Ошибка разбора, неизвестное поле, неподдерживаемая версия и неверный `default` несут `ErrInvalidConfig`; `authz.Load` открывает файл и передаёт его в `authz.Parse`. Если в `gobellim.Config` передан `Policy: nil`, `gobellim.New` возвращает `ErrInvalidConfig`.

Проверка файла без запуска сервиса:

```sh
kitctl policy check ./policy.yaml
```

## Смотрите также

- [Справочник gobellim](./gobellim.md) — `gobellim.New` и поле `Policy`
- [Защита](../guard/README.md) — рельсы и вердикты
- [Ошибки](./errors.md) — `ErrUnauthorized` и `ErrInvalidConfig`
