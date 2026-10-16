import Link from "fumadocs-core/link";
import defaultMdxComponents from "fumadocs-ui/mdx";
import type { MDXComponents } from "mdx/types";
import type { ComponentProps } from "react";

import { resolveDocLink } from "@/lib/links";

/**
 * `file` is the source markdown path (e.g. `guides/tool-calling.md`), which is
 * what the relative links in the content are written against.
 */
export function getMDXComponents(file: string, components?: MDXComponents) {
  const Anchor = ({ href, ...props }: ComponentProps<"a">) => (
    <Link {...props} href={(href && resolveDocLink(file, href)) ?? href} />
  );

  return {
    ...defaultMdxComponents,
    a: Anchor,
    ...components,
  } satisfies MDXComponents;
}

export function useMDXComponents(components?: MDXComponents): MDXComponents {
  return { ...defaultMdxComponents, ...components };
}

declare global {
  type MDXProvidedComponents = ReturnType<typeof getMDXComponents>;
}
