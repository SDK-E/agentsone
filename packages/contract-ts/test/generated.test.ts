// SPDX-License-Identifier: MIT
import { readFile, readdir } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

const generated = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../src/generated");

async function listFiles(dir: string): Promise<string[]> {
  const entries = await readdir(dir, { recursive: true, withFileTypes: true });
  return entries.filter((e) => e.isFile()).map((e) => path.join(e.parentPath, e.name));
}

describe("generated files", () => {
  it("include the OpenAPI types and the schema index", async () => {
    const files = await listFiles(generated);
    expect(files).toContain(path.join(generated, "openapi.ts"));
    expect(files).toContain(path.join(generated, "schemas/index.ts"));
  });

  it("each start with an SPDX line and a do-not-edit marker", async () => {
    for (const file of await listFiles(generated)) {
      const head = (await readFile(file, "utf8")).split("\n").slice(0, 5).join("\n");
      expect(head, file).toContain("SPDX-License-Identifier: MIT");
      expect(head, file).toMatch(/Code generated .* DO NOT EDIT\./);
    }
  });
});
