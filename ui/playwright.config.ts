// SPDX-License-Identifier: MIT
//
// Every page, in every state, in light and dark, at phone and desktop width (TESTING.md B7c).
// The interface is the production build, served by `vite preview`, talking to the fake daemon.
import { fileURLToPath } from "node:url";

import { defineConfig } from "@playwright/test";

const repoRoot = fileURLToPath(new URL("..", import.meta.url));
const FAKE = "127.0.0.1:7799";

const sizes = {
  phone: { width: 390, height: 844 },
  desktop: { width: 1440, height: 900 },
};

export default defineConfig({
  testDir: "e2e",
  outputDir: "test-results",
  retries: 0,
  forbidOnly: true,
  fullyParallel: true,
  reporter: process.env.CI ? [["list"], ["github"]] : "list",
  use: { baseURL: "http://127.0.0.1:4173", trace: "off" },
  projects: (["light", "dark"] as const).flatMap((colorScheme) =>
    Object.entries(sizes).map(([size, viewport]) => ({
      name: `${size}-${colorScheme}`,
      use: { viewport, colorScheme, reducedMotion: "reduce" as const },
    })),
  ),
  webServer: [
    {
      command: `go run ./cmd/agentsoned-fake -addr ${FAKE} -fixtures contract/fixtures/api -cors-origin http://127.0.0.1:4173 -stream-interval 1s`,
      cwd: repoRoot,
      url: `http://${FAKE}/v1/setup`,
      reuseExistingServer: false,
      timeout: 180_000,
    },
    {
      command: "pnpm exec vite preview",
      env: { AGENTSONE_API: `http://${FAKE}` },
      url: "http://127.0.0.1:4173",
      reuseExistingServer: false,
      timeout: 60_000,
    },
  ],
});
