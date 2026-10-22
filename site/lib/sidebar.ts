import type { Folder, Node } from "fumadocs-core/page-tree";

/**
 * Sidebar order and labels. The keys are content paths inside `docs/`: pages as
 * `"<dir>/<file>.md"`, folders as `"<dir>"`. Everything else falls back to the
 * default alphabetical order, so an unlisted page is never hidden.
 *
 * Mirrors the reading order in `docs/README.md`.
 */
const ORDER = [
  "README.md",
  "overview.md",
  "installation.md",
  "tools",
  "tools/README.md",
  "tools/implementation.md",
  "workflows",
  "workflows/README.md",
  "workflows/session.md",
  "workflows/implementation.md",
  "guard",
  "guard/README.md",
  "guard/implementation.md",
  "tracing",
  "tracing/README.md",
  "tracing/implementation.md",
  "retrieval",
  "retrieval/README.md",
  "retrieval/implementation.md",
  "providers",
  "providers/README.md",
  "providers/implementation.md",
  "memory",
  "memory/README.md",
  "memory/implementation.md",
  "mcp",
  "mcp/README.md",
  "mcp/implementation.md",
  "plugins",
  "plugins/README.md",
  "plugins/l2-onnx.md",
  "plugins/embeddings.md",
  "plugins/elasticsearch.md",
  "plugins/hybrid-search.md",
  "reference",
  "reference/gobellim.md",
  "reference/configuration.md",
  "reference/policy.md",
  "reference/providers.md",
  "reference/elasticsearch.md",
  "reference/errors.md",
];

const RANK = new Map(ORDER.map((key, index) => [key, index]));

/** Folder name shown in the sidebar; without this it is the folder name itself. */
const LABELS: Record<string, string> = {
  tools: "Инструменты",
  workflows: "Рабочие процессы",
  guard: "Защита",
  tracing: "Трассировка",
  retrieval: "Поиск и индексация",
  providers: "Провайдеры",
  memory: "Память",
  mcp: "MCP",
  plugins: "Плагины",
  reference: "Справочник",
};

function keyOf(node: Node): string {
  if (node.type === "folder") return node.$ref?.folder ?? "";
  if (node.type === "page") return node.$ref ?? "";
  return "";
}

function rankOf(node: Node): number {
  return RANK.get(keyOf(node)) ?? Number.MAX_SAFE_INTEGER;
}

// Typed structurally rather than as `PageTreeTransformer`, so it stays
// assignable to the loader's concrete content-storage type.
export const sidebarOrder = {
  folder(node: Folder): Folder {
    const path = node.$ref?.folder ?? "";
    const label = LABELS[path];
    if (label) node.name = label;
    // A stable sort, so unranked siblings keep the default path order.
    node.children = [...node.children].sort((a, b) => rankOf(a) - rankOf(b));
    return node;
  },
};
