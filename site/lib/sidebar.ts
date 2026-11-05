import type { Folder, Node } from "fumadocs-core/page-tree";

/**
 * The docs tree is intentionally small: one how-to page. The runnable examples
 * live in `examples/notebooks` and are linked from the page.
 */
const ORDER = ["README.md"];

const RANK = new Map(ORDER.map((key, index) => [key, index]));

function keyOf(node: Node): string {
  if (node.type === "folder") return node.$ref?.folder ?? "";
  if (node.type === "page") return node.$ref ?? "";
  return "";
}

function rankOf(node: Node): number {
  return RANK.get(keyOf(node)) ?? Number.MAX_SAFE_INTEGER;
}

export const sidebarOrder = {
  folder(node: Folder): Folder {
    node.children = [...node.children].sort((a, b) => rankOf(a) - rankOf(b));
    return node;
  },
};
