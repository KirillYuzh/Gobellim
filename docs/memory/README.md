# Обзор

Память (memory)

gobellim поддерживает три вида памяти: историю диалога, рабочую память и recall.

## История диалога (thread memory)

Сообщения хранятся в Elasticsearch с привязкой к `tenant`, `user_id` и `thread_id`.
При загрузке через `memoryLoad` возвращаются последние сообщения потока.

## Рабочая память (working memory)

Один структурированный документ на поток. Ключ — `MemoryWorkingID(tenant, userID,
threadID)`. Два пользователя одного тенанта с одинаковым `thread_id` НЕ делят
working memory — данные скоупились по `user_id`.

## Recall

kNN поиск по сообщениям потока с вектором запроса. Применяются фильтры tenant
и user_id.

## Смотрите также

- [Быстрый старт](../overview.md)
- [Справочник API](../reference/gobellim.md) — `gobellim.MemoryLoad`, `gobellim.MemoryAppend`, `gobellim.MemoryRecall`, `gobellim.MemoryWorking`, `gobellim.MemorySetWorking`

Как это устроено → [implementation.md](./implementation.md)
