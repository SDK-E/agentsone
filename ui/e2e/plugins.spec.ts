// SPDX-License-Identifier: MIT
import { expect, test } from "@playwright/test";

import { checkPage, STATES, useScenario, type Scenario } from "./harness";
import { openPanel } from "./open-panel";

const scenarios: Record<(typeof STATES)[number], Scenario> = {
  empty: { all: "empty" },
  loaded: {},
  error: { only: { listPlugins: "error", searchPluginCatalog: "error" } },
  "long-text": { all: "long-text" },
};

for (const state of STATES) {
  test(`plugins, ${state}`, async ({ page }, testInfo) => {
    const clean = await useScenario(page, scenarios[state]);
    await openPanel(page, "Plugins");

    if (state === "empty") {
      await expect(page.getByRole("heading", { name: "No plugins installed" })).toBeVisible();
      await expect(page.getByRole("heading", { name: "Nothing to install" })).toBeVisible();
    } else if (state === "error") {
      await expect(page.getByRole("alert").first()).toBeVisible();
    } else {
      await expect(
        page.getByRole("list", { name: "Installed plugins" }).getByRole("listitem").first(),
      ).toBeVisible();
    }
    await checkPage(page, testInfo, `plugins-${state}`);
    clean();
  });
}

test("plugins, says why one is not working", async ({ page }, testInfo) => {
  const clean = await useScenario(page, { only: { listPlugins: "long-text" } });
  await openPanel(page, "Plugins");

  const detail = page.getByText(/The server stopped answering at 14:02/);
  await expect(detail).toBeVisible();
  await detail.scrollIntoViewIfNeeded();
  await page.screenshot({
    path: `screenshots/${testInfo.project.name}/plugins-health-viewport.png`,
  });
  await checkPage(page, testInfo, "plugins-health-detail");
  clean();
});

test("plugins, turning one off fails", async ({ page }, testInfo) => {
  const clean = await useScenario(page, { only: { disablePlugin: "error" } });
  await openPanel(page, "Plugins");

  await page
    .getByRole("button", { name: /^Turn off / })
    .first()
    .click();

  await expect(page.getByRole("alert").first()).toBeVisible();
  await checkPage(page, testInfo, "plugins-turn-off-failed");
  clean();
});

test("plugins, remove asks first", async ({ page }, testInfo) => {
  const clean = await useScenario(page, {});
  await openPanel(page, "Plugins");

  await page
    .getByRole("button", { name: /^Remove / })
    .first()
    .click();

  await expect(page.getByRole("button", { name: "Keep it" })).toBeVisible();
  await checkPage(page, testInfo, "plugins-remove-confirm");
  clean();
});

test("plugins, install from the catalog", async ({ page }, testInfo) => {
  const clean = await useScenario(page, {});
  await openPanel(page, "Plugins");

  await page
    .getByRole("button", { name: /^Install / })
    .first()
    .click();

  await expect(page.getByRole("status").first()).toBeVisible();
  await checkPage(page, testInfo, "plugins-installed");
  clean();
});

test("plugins, install fails", async ({ page }, testInfo) => {
  const clean = await useScenario(page, { only: { installPlugin: "error" } });
  await openPanel(page, "Plugins");

  await page
    .getByRole("button", { name: /^Install / })
    .first()
    .click();

  await expect(page.getByRole("alert").first()).toBeVisible();
  await checkPage(page, testInfo, "plugins-install-failed");
  clean();
});
