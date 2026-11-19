// SPDX-License-Identifier: MIT
import axe from "axe-core";
import { expect } from "vitest";

/**
 * Check a rendered tree against WCAG 2.2 AA. jsdom cannot compute colour contrast, so that rule
 * is proven in the Playwright run instead (TESTING.md B7b).
 */
export async function expectNoAxeViolations(container: Element): Promise<void> {
  const result = await axe.run(container, {
    runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"] },
    rules: { "color-contrast": { enabled: false } },
  });
  expect(result.violations.map((v) => `${v.id}: ${v.help}`)).toEqual([]);
}
