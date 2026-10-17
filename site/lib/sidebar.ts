import type { Folder, Node } from "fumadocs-core/page-tree";

/**
 * Sidebar order and labels. The keys are content paths inside `docs/`: pages as
 * `"<dir>/<file>.md"`, folders as `"<dir>"`. Everything else falls back to the
 * default alphabetical order, so an unlisted page is never hidden.
 *
 * Mirrors the reading order in `docs/README.md` and `docs/guides/README.md`.
 */
const ORDER = [
  "README.md",
  "getting-started",
  "getting-started/installation.md",
  "getting-started/quickstart.md",
  "getting-started/project-structure.md",
  "concepts",
  "concepts/chat.md",
  "concepts/streaming.md",
  "concepts/tools.md",
  "concepts/mcp.md",
  "concepts/retrieval.md",
  "concepts/memory.md",
  "concepts/guardrails.md",
  "concepts/access-control.md",
  "concepts/security-model.md",
  "concepts/observability.md",
  "concepts/eval.md",
  "concepts/flow.md",
  "guides",
  "guides/README.md",
  "guides/providers",
  "guides/providers/overview.md",
  "guides/providers/openai-compatible.md",
  "guides/providers/ollama.md",
  "guides/providers/vllm.md",
  "guides/providers/gigachat.md",
  "guides/providers/embeddings.md",
  "guides/structured-output.md",
  "guides/tool-calling.md",
  "guides/guardrails.md",
  "guides/retrieval.md",
  "guides/observability.md",
  "guides/evals.md",
  "guides/pipelines.md",
  "guides/security.md",
  "guides/errors.md",
  "guides/deployment.md",
  "guides/performance.md",
  "guides/plugins.md",
  "reference",
  "reference/kit.md",
  "reference/providers.md",
  "reference/policy.md",
  "reference/configuration.md",
  "reference/elasticsearch.md",
  "reference/errors.md",
  "internals",
  "internals/architecture.md",
  "internals/design.md",
  "internals/business-rules.md",
  "internals/decisions.md",
  "internals/plan.md",
  "internals/references.md",
  "SECURITY.md",
];

const RANK = new Map(ORDER.map((key, index) => [key, index]));

/** Folder name shown in the sidebar; without this it is the folder name itself. */
const LABELS: Record<string, string> = {
  "getting-started": "Начало работы",
  concepts: "Основы",
  guides: "Гайды",
  "guides/providers": "Провайдеры",
  reference: "Справочник",
  internals: "Устройство",
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
