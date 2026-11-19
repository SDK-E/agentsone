// SPDX-License-Identifier: MIT
import { fileURLToPath } from "node:url";

import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

const repoRoot = fileURLToPath(new URL("..", import.meta.url));
// The API the dev server and the preview server forward /v1 to. The fake daemon listens here.
const apiTarget = process.env.AGENTSONE_API ?? "http://127.0.0.1:7700";
const proxy = { "/v1": { target: apiTarget, changeOrigin: false } };

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      // The contract package ships built files; the interface compiles its source directly.
      "@agentsone/contract": fileURLToPath(
        new URL("../packages/contract-ts/src/index.ts", import.meta.url),
      ),
    },
  },
  server: { host: "127.0.0.1", port: 5173, strictPort: true, proxy, fs: { allow: [repoRoot] } },
  preview: { host: "127.0.0.1", port: 4173, strictPort: true, proxy },
  build: { outDir: "dist", emptyOutDir: true, sourcemap: false, assetsInlineLimit: 0 },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    include: ["src/**/*.test.{ts,tsx}"],
    restoreMocks: true,
    coverage: {
      provider: "v8",
      include: ["src/**/*.{ts,tsx}"],
      exclude: ["**/*.test.*", "src/test/**", "src/main.tsx", "**/generated/**"],
      thresholds: { statements: 80 },
    },
  },
});
