// SPDX-License-Identifier: MIT
import { expect, test } from "@playwright/test";

import { checkPage, STATES, useScenario, type Scenario } from "./harness";
import { openPanel } from "./open-panel";

const scenarios: Record<(typeof STATES)[number], Scenario> = {
  empty: { all: "empty" },
  loaded: {},
  error: { only: { listMemory: "error" } },
  "long-text": { all: "long-text" },
};

for (const state of STATES) {
  test(`memory, ${state}`, async ({ page }, testInfo) => {
    const clean = await useScenario(page, scenarios[state]);
    await openPanel(page, "Memory");

    if (state === "empty") {
      await expect(page.getByRole("heading", { name: "Nothing remembered yet" })).toBeVisible();
    } else if (state === "error") {
      await expect(page.getByRole("alert")).toBeVisible();
    } else {
      await expect(
        page
          .getByRole("list", { name: "Memory notes, newest first" })
          .getByRole("listitem")
          .first(),
      ).toBeVisible();
    }
    await checkPage(page, testInfo, `memory-${state}`);
    clean();
  });
}

test("memory, what the screen shows before and after scrolling", async ({ page }, testInfo) => {
  const clean = await useScenario(page, {});
  await openPanel(page, "Memory");

  const list = page.getByRole("list", { name: "Memory notes, newest first" });
  await expect(list.getByRole("listitem").first()).toBeVisible();
  await page.screenshot({ path: `screenshots/${testInfo.project.name}/memory-viewport.png` });

  const panel = page.getByRole("tabpanel", { name: "Memory" });
  await panel.evaluate((el) => {
    el.scrollTop = el.scrollHeight;
  });
  await page.screenshot({
    path: `screenshots/${testInfo.project.name}/memory-viewport-scrolled.png`,
  });
  clean();
});

test("memory, read a note", async ({ page }, testInfo) => {
  const clean = await useScenario(page, {});
  await openPanel(page, "Memory");

  await page.getByRole("button", { name: /How this society works/ }).click();

  await expect(page.getByRole("heading", { name: "How this society works" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Delete this note" })).toBeVisible();
  await checkPage(page, testInfo, "memory-note");
  clean();
});

test("memory, search", async ({ page }, testInfo) => {
  const clean = await useScenario(page, {});
  await openPanel(page, "Memory");

  await page.getByRole("searchbox", { name: "Search memory" }).fill("society");
  await page.getByRole("button", { name: "Search", exact: true }).click();

  await expect(
    page
      .getByRole("list", { name: "Search results, best match first" })
      .getByRole("listitem")
      .first(),
  ).toBeVisible();
  await checkPage(page, testInfo, "memory-search");
  clean();
});

test("memory, search finds nothing", async ({ page }, testInfo) => {
  const clean = await useScenario(page, { only: { searchMemory: "empty" } });
  await openPanel(page, "Memory");

  await page.getByRole("searchbox", { name: "Search memory" }).fill("zebra");
  await page.getByRole("button", { name: "Search", exact: true }).click();

  await expect(page.getByRole("heading", { name: "Nothing found" })).toBeVisible();
  await checkPage(page, testInfo, "memory-search-empty");
  clean();
});
