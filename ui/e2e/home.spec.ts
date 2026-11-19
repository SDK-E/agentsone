// SPDX-License-Identifier: MIT
import { expect, test } from "@playwright/test";

import { checkPage, STATES, useScenario, type Scenario } from "./harness";

const scenarios: Record<(typeof STATES)[number], Scenario> = {
  empty: { all: "empty" },
  loaded: {},
  error: { only: { listLiveList: "error" } },
  "long-text": { all: "long-text" },
};

for (const state of STATES) {
  test(`home, ${state}`, async ({ page }, testInfo) => {
    const clean = await useScenario(page, scenarios[state]);
    await page.goto("/");

    await expect(page.getByRole("heading", { name: "Ask the society" })).toBeVisible();
    const society = page.getByRole("region", { name: "The society" });
    if (state === "empty") {
      await expect(society.getByRole("heading", { name: "Nobody lives here yet" })).toBeVisible();
    } else if (state === "error") {
      await expect(society.getByRole("alert")).toBeVisible();
    } else {
      await expect(society.getByRole("listitem").first()).toBeVisible();
    }
    await checkPage(page, testInfo, `home-${state}`);
    clean();
  });
}

test("home, org chart", async ({ page }, testInfo) => {
  const clean = await useScenario(page, {});
  await page.goto("/");

  await page.getByRole("button", { name: "Org chart" }).click();

  await expect(page.getByRole("list", { name: "Org chart, who hired whom" })).toBeVisible();
  await checkPage(page, testInfo, "home-org-chart");
  clean();
});

test("home, settings panel", async ({ page }, testInfo) => {
  const clean = await useScenario(page, {});
  await page.goto("/");
  if ((page.viewportSize()?.width ?? 0) < 768) {
    await page.getByRole("button", { name: "Open panels" }).click();
  }

  await page.getByRole("tab", { name: "Settings" }).click();

  await expect(page.getByRole("radio", { name: "System" })).toBeChecked();
  await page.getByText("Dark", { exact: true }).click();
  await expect(page.getByRole("radio", { name: "Dark" })).toBeChecked();
  await expect(page.locator("html")).toHaveAttribute("data-mode", "dark");
  await checkPage(page, testInfo, "home-settings");
  clean();
});

test("home, route a request to an agent's chat", async ({ page }, testInfo) => {
  const clean = await useScenario(page, {});
  await page.goto("/");

  await page
    .getByRole("textbox", { name: "What do you need?" })
    .fill("Can you add a page about the budget walls to the docs site?");
  await page.getByRole("button", { name: "Ask" }).click();

  await expect(page).toHaveURL(/\/agents\/builder\/chat$/);
  await expect(page.getByRole("heading", { name: "Why Builder" })).toBeVisible();
  await expect(page.getByText("Owns the docs site goal")).toBeVisible();
  await checkPage(page, testInfo, "chat-after-routing");
  clean();
});

test("home, routing fails", async ({ page }, testInfo) => {
  const clean = await useScenario(page, { only: { routeRequest: "error" } });
  await page.goto("/");

  await page.getByRole("textbox", { name: "What do you need?" }).fill("Back up my files");
  await page.getByRole("button", { name: "Ask" }).click();

  await expect(page.getByRole("alert")).toContainText("The router agent is restarting");
  await checkPage(page, testInfo, "home-routing-error");
  clean();
});
