import { createFromSource } from "fumadocs-core/search/server";

import { source } from "@/lib/source";

// `staticGET` bakes the index at build time, which is what `output: 'export'`
// needs: there is no server to answer search queries at runtime.
export const revalidate = false;
export const { staticGET: GET } = createFromSource(source);
