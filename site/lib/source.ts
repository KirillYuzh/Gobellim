import { loader, type LoaderOptions } from "fumadocs-core/source";
import { pageSchema } from "fumadocs-core/source/schema";
import { defineDocs } from "fumadocs-mdx/macro";
import { z } from "zod";

import { sidebarOrder } from "./sidebar";

// Our pages carry no frontmatter. `title` must stay a `string` for the page
// type, so it defaults to "" and the compiler fills it from the first H1.
const docs = defineDocs({
  dir: "../docs",
  docs: { schema: pageSchema.extend({ title: z.string().default("") }) },
});

/** `README.md` is its folder's index page, so it drops out of the slug. */
const slugs: LoaderOptions["slugs"] = (_file, next) => {
  const parts = next();
  return parts[parts.length - 1] === "README" ? parts.slice(0, -1) : parts;
};

export const source = loader({
  // The docs live at the site root: `/` is `docs/README.md`.
  baseUrl: "",
  url: (slugs) => `/${slugs.join("/")}`,
  source: docs.toFumadocsSource(),
  slugs,
  pageTree: { transformers: [sidebarOrder] },
});
