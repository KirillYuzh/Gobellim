# План: docs-сайт на Fumadocs

Цель: сайт документации, который собирается **строго** из markdown-файлов в `docs/`,
в той же иерархии папок, что лежит в репозитории.

Легенда: `[ ]` todo · `[~]` в работе · `[x]` сделано · `[!]` риск/известная слабость.

## Решения владельца

| #   | Вопрос                             | Решение                                                            |
| --- | ---------------------------------- | ------------------------------------------------------------------ |
| 1   | Фреймворк                          | самый простой путь → **Next.js (App Router) + `output: 'export'`** |
| 2   | Порядок страниц                    | **вариант A**: порядок в конфиге сайта, `docs/` не трогаем         |
| 3   | Если `dir: '../docs'` не заведётся | **первое** → приложение в корень репозитория                       |
| 4   | Заголовки                          | `title` необязательный, заголовок берётся из первого `# H1`        |

Размещение: приложение в `site/`, логотипы в `site/public/`. Решение 3 **не потребовалось**
(см. ниже).

## Что проверено на реальной сборке

| Факт                                          | Как проверено                                                             |
| --------------------------------------------- | ------------------------------------------------------------------------- |
| контент из `../docs` собирается               | 49 статических страниц в `site/out`                                       |
| `title` берётся из H1, правок в 47 файлах нет | `<title>Документация kit — Gobellim</title>`, `<h1>Документация kit</h1>` |
| `README.md` — индекс папки                    | `docs/README.md` → `/`, `docs/guides/README.md` → `/guides`               |
| URL чистые, без префикса `/docs`              | `● /`, `● /SECURITY`, `● /concepts/access-control`                        |
| порядок из конфига сайта работает             | сайдбар: `Документация kit`, «Начало работы», «Основы», …                 |
| ссылки переписаны                             | `.md` внутри `docs/` → маршруты; за пределами → GitHub blob               |
| картинки подхватываются                       | `../architecture.svg` → `_next/static/media/architecture.*.svg`           |
| логотип и favicon                             | `one-line-black`/`-white` в шапке, `mascot` в favicon                     |
| поиск                                         | статический индекс `out/api/search`                                       |

## Что пришлось решить по ходу

- **`dir: '../docs'` упирается в Turbopack**: `webpack loader path ... is outside configured
roots`. Лечится одной строкой — `turbopack: { root: repoRoot }` в `next.config.mjs`.
  Поэтому решение 3 (переезд в корень) не понадобилось, и `site/` с логотипами остаётся.
- **`README.md` не индексируется сам по себе**: имя индексной страницы задаётся
  `meta.json` → `pagesIndex`. Вместо 7 файлов `meta.json` внутри `docs/` сделан
  override `slugs` в `lib/source.ts`.
- **Порядок и подписи папок** — трансформер `pageTree.transformers` в `lib/sidebar.ts`
  (одним списком + картой подписей). `docs/` не тронут.
- **`.md`-ссылки Fumadocs не переписывает** — переписываются в компоненте `a`
  (`components/mdx.tsx` + `lib/links.ts`); исходный файл страницы приходит в
  `getMDXComponents(page.path)`.
- **`createContext` в MDX нельзя** (серверный компонент) — поэтому путь файла передаётся
  аргументом, без React-контекста.

## Шаги

- [x] **1. Скелет в `site/`**: Next.js 16 + `fumadocs-mdx` + `fumadocs-core` + `fumadocs-ui`, Tailwind v4, `output: 'export'`, `turbopack.root` = корень репозитория.
- [x] **2. Контент из `docs/`** — работает.
- [x] **3.** Фолбэк не понадобился.
- [x] **4. Заголовки** из H1 (`pageSchema.extend({ title: z.string().default("") })`).
- [x] **5. Порядок и подписи папок** (вариант A, `lib/sidebar.ts`).
- [x] **6. Ссылки**: внутри `docs/` → маршрут, за пределами → GitHub blob.
- [x] **7. Индексы папок**: `README.md` через override `slugs`.
- [x] **8. Шелл**: `DocsLayout`, логотип (переключение по теме), favicon, TOC, prev/next, GitHub.
- [x] **9. Поиск**: статический индекс (`staticGET`), `out/api/search`.
- [x] **10. Уборка**: упоминания `_session/` убраны из `AGENTS.md`, `_`-skip в `docslint` снят.
- [x] **11. Гейт**: `npm run build` + Go-гейт.

## Известные слабости (на потом)

- [!] **Тело страницы рендерится на клиенте** (Streamdown парсит markdown в браузере), поэтому
  содержимое не попадает в статический HTML, как было с fumadocs-mdx. Плюс в бандл
  приходит Shiki и mermaid (лениво).
- [!] **Индекс поиска ≈6.2 МБ** (режим ZBSearch «advanced»). Грузится лениво, только при
  открытии поиска, но это много. Уменьшение — `SimpleOptions`/`initSimpleSearch` из
  `fumadocs-core/search/server`.
- [!] **Слаг-правила расходятся**: Fumadocs оставляет двойные дефисы
  (`10-guard-результата--последним`), `docslint` их схлопывает. Текущие ссылки на якоря
  совпадают, но правило стоит свести в одну сторону.
- [!] Поиск не локализован (UI-строки Fumadocs англоязычные); `translations` у
  `RootProvider` — при желании.
- [!] Проверки `docslint` и сборка сайта не связаны: если `docs/` поедет, сайт упадёт
  только на своей сборке. Возможен CI-джоб `site`.

## Рендер тела страницы: Streamdown

По решению владельца markdown рендерит Streamdown, а не скомпилированный MDX.

- `app/(docs)/[[...slug]]/page.tsx` читает сырой `docs/<page.path>` через `fs` и отдаёт
  строку в клиентский `components/markdown.tsx`.
- В `components/markdown.tsx`: `plugins={{ code, mermaid }}` (подсветка Shiki + **mermaid
  из коробки**), `linkSafety: { enabled: false }`, rehype-плагин `headingIds`
  (`lib/markdown.ts`) и подмена компонентов `a`/`img`.
- `lib/markdown.ts` — `slugify`/`outline`/`headingIds`; `outline` кормит TOC, который
  передаётся в `DocsPage`. Слаггер повторяет `scripts/docslint`, поэтому расхождение
  слаггеров, о котором было ниже, для сайта снято.
- Картинки: `scripts/sync-docs-assets.mjs` копирует изображения из `../docs` в
  `public/docs-assets/` (в статическом экспорте middleware нет), `docAssetUrl` строит URL.
  Копия генерируется на `predev`/`prebuild` и в git не попадает.
- Fumadocs остаётся источником данных: дерево, заголовки, порядок и подписи
  (`lib/sidebar.ts`), поиск (`out/api/search`). Markdown обрабатывается дважды — это
  осознанная цена решения.
- `components/mdx.tsx` удалён (стал мёртвым кодом).

## Заметки

- Сайт не участвует в `go build ./...`; `site/node_modules`, `site/.next`, `site/out`
  в `site/.gitignore`.
- Mermaid рендерит Streamdown (`@streamdown/mermaid`) — 27 блоков в 21 файле.
