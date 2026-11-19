// SPDX-License-Identifier: MIT
import { describe, expect, it } from "vitest";

import { applyTheme, defaultTheme, resolveMode, themeVariables } from "./theme";

describe("resolveMode", () => {
  it.each([
    ["system", false, "light"],
    ["system", true, "dark"],
    ["light", true, "light"],
    ["dark", false, "dark"],
  ] as const)("%s with dark preference %s is %s", (mode, prefersDark, expected) => {
    expect(resolveMode(mode, prefersDark)).toBe(expected);
  });
});

describe("themeVariables", () => {
  it("turns colour roles into kebab-case custom properties for the chosen mode", () => {
    const light = themeVariables(defaultTheme, "light");
    const dark = themeVariables(defaultTheme, "dark");

    expect(light["--color-surface-raised"]).toBe(defaultTheme.modes.light.color.surfaceRaised);
    expect(dark["--color-on-accent"]).toBe(defaultTheme.modes.dark.color.onAccent);
    expect(light["--color-background"]).not.toBe(dark["--color-background"]);
  });

  it("carries type, radius, spacing, shadow and motion tokens", () => {
    const vars = themeVariables(defaultTheme, "light");

    expect(vars["--font-sans"]).toContain("Atkinson Hyperlegible Next");
    expect(vars["--text-md"]).toBe("1rem");
    expect(vars["--leading-md"]).toBe("1.5");
    expect(vars["--weight-bold"]).toBe("700");
    expect(vars["--radius-lg"]).toBe("14px");
    expect(vars["--space-md"]).toBe("16px");
    expect(vars["--shadow-md"]).toBe(defaultTheme.modes.light.shadow.md);
    expect(vars["--motion-normal"]).toBe("200ms");
  });
});

describe("applyTheme", () => {
  it("writes the variables, the colour scheme and the mode onto the element", () => {
    const root = document.createElement("div");

    applyTheme(root, defaultTheme, "dark");

    expect(root.style.getPropertyValue("--color-text")).toBe(defaultTheme.modes.dark.color.text);
    expect(root.style.getPropertyValue("color-scheme")).toBe("dark");
    expect(root.dataset.mode).toBe("dark");
  });
});
