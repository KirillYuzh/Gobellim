import { createMDX } from "fumadocs-mdx/next";

/**
 * The docs live in `../docs`, outside this app's directory, so Turbopack has to
 * be told that the repository root is the project root.
 */
const repoRoot = new URL("..", import.meta.url).pathname;

/** @type {import('next').NextConfig} */
const config = {
  reactStrictMode: true,
  output: "export",
  images: { unoptimized: true },
  turbopack: { root: repoRoot },
};

export default createMDX()(config);
