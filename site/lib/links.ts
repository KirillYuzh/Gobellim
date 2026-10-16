export const repoUrl = "https://github.com/KirillYuzh/Gobellim";

const REPO_BLOB = `${repoUrl}/blob/main`;
const DOCS = "docs/";

/**
 * Rewrites a link written against the markdown tree into a link that works in
 * the site.
 *
 * - `#anchor`, absolute and external targets are left alone (`null`);
 * - a relative target that stays inside `docs/` becomes its route
 *   (`../reference/kit.md#generate` -> `/reference/kit#generate`);
 * - anything else resolves to a repository file and points at GitHub, because
 *   it is not part of the site (the repo README, `tools/gliner`, the demo).
 */
export function resolveDocLink(fromFile: string, href: string): string | null {
  if (!href || href.startsWith("/") || href.startsWith("#") || isExternal(href))
    return null;

  const hashAt = href.indexOf("#");
  const target = hashAt >= 0 ? href.slice(0, hashAt) : href;
  const hash = hashAt >= 0 ? href.slice(hashAt + 1) : "";
  if (target === "") return null;

  const repoPath = resolveInRepo(fromFile, target);
  if (!repoPath.startsWith(DOCS)) return `${REPO_BLOB}/${repoPath}`;

  let slug = repoPath.slice(DOCS.length).replace(/\.md$/, "");
  if (slug === "README") slug = "";
  else if (slug.endsWith("/README")) slug = slug.slice(0, -"/README".length);

  return `/${slug}${hash ? `#${hash}` : ""}`;
}

/**
 * Resolves `target` against the directory of `fromFile`, in repository paths:
 * a file at `guides/tool-calling.md` is rooted at `docs/guides`.
 */
function resolveInRepo(fromFile: string, target: string): string {
  const at = fromFile.lastIndexOf("/");
  const parts =
    at < 0
      ? [DOCS.slice(0, -1)]
      : [DOCS.slice(0, -1), ...fromFile.slice(0, at).split("/")];
  for (const segment of target.split("/")) {
    if (segment === "" || segment === ".") continue;
    if (segment === "..") parts.pop();
    else parts.push(segment);
  }
  return parts.join("/");
}

function isExternal(href: string): boolean {
  return href.startsWith("//") || /^[a-z][a-z0-9+.-]*:/i.test(href);
}
