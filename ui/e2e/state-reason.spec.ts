// SPDX-License-Identifier: MIT
//
// Why an agent stopped, under its card on the home page and its row in the Monitor, with a short
// reason and with the longest one the contract allows.
import { expect, test, type Locator, type Page } from "@playwright/test";

import { checkPage, useScenario, type Scenario } from "./harness";
import { openPanel } from "./open-panel";

const REASONS = {
  failed: "The model gateway is not reachable.",
  "failed-long-text": "ask a human to look at the edge service log.",
} as const;

const scenarios: Record<keyof typeof REASONS, Scenario> = {
  failed: { all: "failed" },
  "failed-long-text": { all: "failed-long-text" },
};

/** The reason wraps inside its card: nothing sticks out sideways, on the card or the page. */
async function expectNoSidewaysOverflow(page: Page, card: Locator): Promise<void> {
  const fits = await card.evaluate((el) => el.scrollWidth <= el.clientWidth);
  expect(fits, "the card does not scroll sideways").toBe(true);
  const pageFits = await page.evaluate(
    () => document.documentElement.scrollWidth <= document.documentElement.clientWidth,
  );
  expect(pageFits, "the page does not scroll sideways").toBe(true);
}

for (const variant of Object.keys(REASONS) as (keyof typeof REASONS)[]) {
  test(`home, a stopped agent says why, ${variant}`, async ({ page }, testInfo) => {
    const clean = await useScenario(page, scenarios[variant]);
    await page.goto("/");

    const society = page.getByRole("region", { name: "The society" });
    const reason = society.getByText(REASONS[variant]);
    await expect(reason).toBeVisible();
    await expect(society.getByText("Why it stopped:")).toBeVisible();
    await expectNoSidewaysOverflow(page, society.getByRole("listitem").first());
    await checkPage(page, testInfo, `home-stopped-${variant}`);
    clean();
  });

  test(`monitor, a stopped agent says why, ${variant}`, async ({ page }, testInfo) => {
    const clean = await useScenario(page, scenarios[variant]);
    await openPanel(page, "Monitor");

    const panel = page.getByRole("tabpanel", { name: "Monitor" });
    await expect(panel.getByText(REASONS[variant])).toBeVisible();
    await expect(panel.getByText("Why it stopped:")).toBeVisible();
    await expectNoSidewaysOverflow(page, panel.getByRole("listitem").first());
    await checkPage(page, testInfo, `monitor-stopped-${variant}`);
    clean();
  });
}

test("home, nothing extra when no agent has stopped", async ({ page }) => {
  const clean = await useScenario(page, {});
  await page.goto("/");

  await expect(
    page.getByRole("region", { name: "The society" }).getByRole("listitem").first(),
  ).toBeVisible();
  await expect(page.getByText("Why it stopped:")).toHaveCount(0);
  clean();
});
