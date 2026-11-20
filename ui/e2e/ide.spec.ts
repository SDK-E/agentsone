// SPDX-License-Identifier: MIT
import { expect, test, type Page } from "@playwright/test";

import { checkPage, STATES, useScenario, type Scenario } from "./harness";
import { openPanel } from "./open-panel";

/**
 * The daemon proxies the editor at /ide/<human>/ on the interface's own origin, with the same
 * framing headers. The page preview has no editor, so a small stand-in answers there.
 */
async function standInEditor(page: Page) {
  await page.route("**/ide/ada/**", (route) =>
    route.fulfill({
      status: 200,
      contentType: "text/html; charset=utf-8",
      headers: {
        "X-Frame-Options": "SAMEORIGIN",
        "Content-Security-Policy": "frame-ancestors 'self'",
      },
      body: '<!doctype html><html lang="en"><head><title>Editor</title></head><body><main><h1>Editor stand-in</h1></main></body></html>',
    }),
  );
}

const scenarios: Record<(typeof STATES)[number], Scenario> = {
  empty: { all: "empty" },
  loaded: {},
  error: { only: { getIdeServer: "error" } },
  "long-text": { all: "long-text" },
};

for (const state of STATES) {
  test(`ide, ${state}`, async ({ page }, testInfo) => {
    await standInEditor(page);
    const clean = await useScenario(page, scenarios[state]);
    await openPanel(page, "IDE");

    if (state === "empty") {
      await expect(page.getByRole("heading", { name: "Your editor is not running" })).toBeVisible();
      await expect(page.getByRole("button", { name: "Start the editor" })).toBeEnabled();
    } else if (state === "error") {
      await expect(page.getByRole("alert").first()).toBeVisible();
    } else if (state === "long-text") {
      await expect(page.getByRole("alert")).toContainText("the file is not valid JSON");
      await expect(page.getByRole("button", { name: "Try again" })).toBeVisible();
    } else {
      // Same origin through the daemon proxy: the editor loads inside the panel.
      const editor = page.frameLocator('iframe[title="Code editor for ada"]');
      await expect(editor.getByRole("heading", { name: "Editor stand-in" })).toBeVisible();
      await expect(page.getByRole("link", { name: "Open in a new tab" })).toHaveAttribute(
        "href",
        "/ide/ada/",
      );
    }
    await page.getByRole("tabpanel", { name: "IDE" }).evaluate((el) => {
      el.scrollIntoView({ block: "start" });
    });
    await page.screenshot({
      path: `screenshots/${testInfo.project.name}/ide-${state}-viewport.png`,
    });
    await checkPage(page, testInfo, `ide-${state}`);
    clean();
  });
}

test("ide, open in a new tab", async ({ page, context }) => {
  await standInEditor(page);
  const clean = await useScenario(page, {});
  await context.route("**/ide/ada/**", (route) =>
    route.fulfill({ status: 200, contentType: "text/html", body: "<title>Editor</title>" }),
  );
  await openPanel(page, "IDE");

  const opened = context.waitForEvent("page");
  await page.getByRole("link", { name: "Open in a new tab" }).click();
  const tab = await opened;
  await tab.waitForLoadState();
  expect(new URL(tab.url()).pathname).toBe("/ide/ada/");
  // noopener: the editor tab cannot reach back into the interface.
  expect(await tab.evaluate(() => window.opener === null)).toBe(true);
  await tab.close();
  clean();
});
