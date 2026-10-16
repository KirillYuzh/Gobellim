import type { BaseLayoutProps } from "fumadocs-ui/layouts/shared";

import { repoUrl } from "./links";

export function baseOptions(): BaseLayoutProps {
  return {
    nav: { title: <Brand /> },
    githubUrl: repoUrl,
  };
}

/** Two files instead of one, because the wordmark is black or white. */
function Brand() {
  return (
    <>
      <img
        src="/gobellim-one-line-black.svg"
        alt="Gobellim"
        className="brand-logo brand-logo-light"
      />
      <img
        src="/gobellim-one-line-white.svg"
        alt="Gobellim"
        className="brand-logo brand-logo-dark"
      />
    </>
  );
}
