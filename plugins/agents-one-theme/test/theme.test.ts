// SPDX-License-Identifier: MIT
import assert from "node:assert/strict";
import { test } from "node:test";

import { contrastRatio } from "../src/contrast.ts";
import { brandRoles, loadJson, repoPath, validatorFor } from "./helpers.ts";

interface ContrastPair {
  fg: string;
  bg: string;
  minRatio: number;
}

interface Theme {
  name: string;
  modes: Record<"light" | "dark", { color: Record<string, string> }>;
  contrastPairs: ContrastPair[];
}

interface Manifest {
  contents: { themes: { path: string }[] };
}

const pluginDir = repoPath("plugins/agents-one-theme");
const theme = loadJson(`${pluginDir}/theme.json`) as Theme;
const manifest = loadJson(`${pluginDir}/agentsone.plugin.json`) as Manifest;
const modes = ["light", "dark"] as const;

const textPairs = [
  ["text", "background"],
  ["text", "surface"],
  ["text", "surfaceRaised"],
  ["text", "surfaceSunken"],
  ["textMuted", "background"],
  ["textMuted", "surface"],
  ["textMuted", "surfaceRaised"],
  ["textMuted", "surfaceSunken"],
  ["accent", "background"],
  ["accent", "surface"],
  ["onAccent", "accent"],
  ["onAccent", "accentHover"],
  ["onAccentSubtle", "accentSubtle"],
  ["visitor", "background"],
  ["visitor", "surface"],
  ["onVisitorSubtle", "visitorSubtle"],
  ["success", "background"],
  ["success", "surface"],
  ["warning", "background"],
  ["warning", "surface"],
  ["danger", "background"],
  ["danger", "surface"],
  ["onDanger", "danger"],
  ["info", "background"],
  ["info", "surface"],
] as const;

const uiPairs = [
  ["borderStrong", "background"],
  ["borderStrong", "surface"],
  ["focus", "background"],
  ["focus", "surface"],
  ["alive", "background"],
  ["alive", "surface"],
  ["accent", "surfaceSunken"],
] as const;

// The pairs promised in brand/colours/colours.md. The theme must declare every one.
const brandPairs: ContrastPair[] = [
  ...textPairs.map(([fg, bg]) => ({ fg, bg, minRatio: 4.5 })),
  ...uiPairs.map(([fg, bg]) => ({ fg, bg, minRatio: 3 })),
];

const pairKey = (pair: ContrastPair): string =>
  `${pair.fg} on ${pair.bg} at ${String(pair.minRatio)}:1`;

await test("theme validates against the theme schema", () => {
  const validate = validatorFor("theme.schema.json");
  assert.ok(validate(theme), JSON.stringify(validate.errors, null, 2));
});

await test("plugin manifest validates and points at theme.json", () => {
  const validate = validatorFor("plugin-manifest.schema.json");
  assert.ok(validate(manifest), JSON.stringify(validate.errors, null, 2));
  assert.deepEqual(
    manifest.contents.themes.map((entry) => entry.path),
    ["./theme.json"],
  );
});

await test("theme is named Agents One Theme", () => {
  assert.equal(theme.name, "Agents One Theme");
});

await test("theme declares every pair promised in the brand guide", () => {
  const declared = new Set(theme.contrastPairs.map(pairKey));
  assert.deepEqual(
    brandPairs.map(pairKey).filter((key) => !declared.has(key)),
    [],
  );
});

for (const mode of modes) {
  await test(`${mode} colours match brand/colours/tokens.json exactly`, () => {
    assert.deepEqual(theme.modes[mode].color, brandRoles(mode));
  });

  await test(`every declared pair meets its contrast minimum in ${mode} mode`, () => {
    const colors = theme.modes[mode].color;
    const failures = theme.contrastPairs.filter(({ fg, bg, minRatio }) => {
      const fgColor = colors[fg];
      const bgColor = colors[bg];
      assert.ok(fgColor && bgColor, `unknown role in pair ${fg} on ${bg}`);
      return contrastRatio(fgColor, bgColor) < minRatio;
    });
    assert.deepEqual(failures.map(pairKey), []);
  });
}
