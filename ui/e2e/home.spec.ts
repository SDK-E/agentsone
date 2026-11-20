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
  await page.getByRole("radio", { name: "Dark" }).check();
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

test("home, a refresh that fails keeps the page and says so without moving it", async ({
  page,
}) => {
  // The machine answers the first time. Later, who is signed in and the setup stop answering.
  await page.clock.install();
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Ask the society" })).toBeVisible();
  await page.clock.fastForward(6_000);

  const failing = async (route: {
    fulfill: (o: { status: number; body: string }) => Promise<void>;
  }) => route.fulfill({ status: 503, body: "{}" });
  await page.route("**/v1/session/me", failing);
  await page.route("**/v1/setup", failing);

  // Go to a chat and back: the shell asks again, in the background, and is not answered.
  await page.locator('a[href*="/chat"]').first().click();
  await page.getByRole("link", { name: "Home", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Ask the society" })).toBeVisible();
  const line = page.getByText("Can't reach the machine. Retrying…");
  await expect(line).toBeVisible();
  // What was live no longer says so.
  const society = page.getByRole("region", { name: "The society" });
  await expect(society.getByText("Paused")).toBeVisible();
  await expect(society.getByText("Live", { exact: true })).toHaveCount(0);
  const heading = page.getByRole("heading", { name: "Ask the society" });
  // Back from the chat the page may keep its old scroll: start at the top, so places can be compared.
  await page.evaluate(() => {
    window.scrollTo(0, 0);
  });
  const before = await heading.boundingBox();

  // It asks again by itself. Twice more and it is a click as well.
  await expect(page.getByRole("button", { name: "Try again" })).toHaveCount(0);
  for (let round = 0; round < 2; round++) {
    const answered = page.waitForResponse("**/v1/session/me");
    await page.clock.fastForward(10_000);
    await answered;
  }
  const retry = page.getByRole("button", { name: "Try again" });
  await expect(retry).toBeVisible();
  // One row on every width, with the button beside the text.
  const row = await page.locator(".stale").boundingBox();
  expect(row?.height).toBeLessThan(40);
  // The line came and the page did not move.
  expect((await heading.boundingBox())?.y).toBe(before?.y);

  // The machine answers: the line goes, and the page is where it was.
  await page.unroute("**/v1/session/me");
  await page.unroute("**/v1/setup");
  await retry.click();
  await expect(line).toHaveCount(0);
  await expect(retry).toHaveCount(0);
  await expect(society.getByText("Live", { exact: true })).toBeVisible();
  await expect(society.getByText("Paused")).toHaveCount(0);
  expect((await heading.boundingBox())?.y).toBe(before?.y);
});
