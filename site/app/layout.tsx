import { RootProvider } from "fumadocs-ui/provider/next";
import type { Metadata } from "next";
import type { ReactNode } from "react";

import "./globals.css";

export const metadata: Metadata = {
  title: { default: "Gobellim", template: "%s — Gobellim" },
  description:
    "Нативные Go-примитивы для безопасной работы с LLM: поиск, вызов инструментов и guardrails за одним проверяющим права путём вызова.",
  icons: { icon: "/gobellim-mascot.svg" },
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="ru" suppressHydrationWarning>
      <body className="flex min-h-screen flex-col">
        <RootProvider>{children}</RootProvider>
      </body>
    </html>
  );
}
