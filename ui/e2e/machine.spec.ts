// SPDX-License-Identifier: MIT
import { expect, test, type Page } from "@playwright/test";

import { checkPage, STATES, useScenario, type Scenario } from "./harness";
import { openPanel } from "./open-panel";

const LISTS = {
  processes: { radio: "Processes", list: "Processes", empty: "No processes to show" },
  services: { radio: "Services", list: "Services", empty: "No services to show" },
  packages: { radio: "Packages", list: "Installed packages", empty: "No packages found" },
  users: { radio: "Users", list: "Linux users", empty: "No users to show" },
  files: {
    radio: "Files",
    list: "Entries in this folder, folders first",
    empty: "This folder is empty",
  },
  terminals: { radio: "Terminals", list: "Terminal sessions", empty: "No terminals open" },
} as const;

const OPERATION: Record<keyof typeof LISTS, string> = {
  processes: "listProcesses",
  services: "listServices",
  packages: "listPackages",
  users: "listMachineUsers",
  files: "listFiles",
  terminals: "listPtySessions",
};

async function show(page: Page, radio: string) {
  await page.getByRole("radio", { name: radio }).check();
}

/** A viewport-sized picture as well, for the visual review, from the top of the panel. */
async function viewport(page: Page, name: string, project: string, fromTop = true) {
  if (fromTop) {
    const sections = page.getByRole("group", { name: "Show" }).filter({
      has: page.getByRole("radio", { name: "Terminals" }),
    });
    await sections.evaluate((el) => {
      el.scrollIntoView({ block: "start" });
    });
  }
  await page.screenshot({ path: `screenshots/${project}/${name}-viewport.png` });
}

test("machine, usage", async ({ page }, testInfo) => {
  const clean = await useScenario(page, {});
  await openPanel(page, "Machine");

  await expect(page.getByRole("radio", { name: "Usage" })).toBeChecked();
  await expect(page.getByRole("meter", { name: "Processor" })).toBeVisible();
  await viewport(page, "machine-usage", testInfo.project.name);
  await checkPage(page, testInfo, "machine-usage");
  clean();
});

test("machine, usage fails", async ({ page }, testInfo) => {
  const clean = await useScenario(page, { only: { getMachineMetrics: "error" } });
  await openPanel(page, "Machine");

  await expect(page.getByRole("alert").first()).toContainText("Sign in again");
  await viewport(page, "machine-usage-error", testInfo.project.name);
  await checkPage(page, testInfo, "machine-usage-error");
  clean();
});

for (const [section, names] of Object.entries(LISTS) as [
  keyof typeof LISTS,
  (typeof LISTS)[keyof typeof LISTS],
][]) {
  for (const state of STATES) {
    test(`machine, ${section}, ${state}`, async ({ page }, testInfo) => {
      const scenarios: Record<(typeof STATES)[number], Scenario> = {
        empty: { all: "empty" },
        loaded: {},
        error: { only: { [OPERATION[section]]: "error" } },
        "long-text": { all: "long-text" },
      };
      const clean = await useScenario(page, scenarios[state]);
      await openPanel(page, "Machine");
      await show(page, names.radio);

      if (state === "empty") {
        await expect(page.getByRole("heading", { name: names.empty })).toBeVisible();
      } else if (state === "error") {
        // The daemon's own words from the error fixture, not a missing-fixture answer.
        await expect(page.getByRole("alert").first()).toContainText("The machine said:");
      } else {
        await expect(
          page.getByRole("list", { name: names.list }).getByRole("listitem").first(),
        ).toBeVisible();
      }
      await viewport(page, `machine-${section}-${state}`, testInfo.project.name);
      await checkPage(page, testInfo, `machine-${section}-${state}`);
      clean();
    });
  }
}

test("machine, read a file", async ({ page }, testInfo) => {
  const clean = await useScenario(page, {});
  await openPanel(page, "Machine");
  await show(page, "Files");

  await page.getByRole("button", { name: "README.md, File" }).click();
  const view = page.getByRole("region", { name: "/home/agents/builder/site/README.md" });
  await expect(view.getByText(/Built by builder for the society/)).toBeVisible();
  await view.scrollIntoViewIfNeeded();
  await viewport(page, "machine-file-read", testInfo.project.name, false);
  await checkPage(page, testInfo, "machine-file-read");
  clean();
});

test("machine, a file too large to read", async ({ page }, testInfo) => {
  const clean = await useScenario(page, { only: { readFile: "error" } });
  await openPanel(page, "Machine");
  await show(page, "Files");

  await page.getByRole("button", { name: "README.md, File" }).click();
  const alert = page.getByRole("alert");
  await expect(alert).toContainText("reads stop at 8 MiB");
  await alert.scrollIntoViewIfNeeded();
  await viewport(page, "machine-file-error", testInfo.project.name, false);
  await checkPage(page, testInfo, "machine-file-error");
  clean();
});

test("machine, open a terminal", async ({ page }, testInfo) => {
  const clean = await useScenario(page, {});
  await openPanel(page, "Machine");
  await show(page, "Terminals");

  await page.getByRole("button", { name: "Open a terminal" }).click();
  await expect(page.getByRole("status").filter({ hasText: "A terminal is open" })).toBeVisible();
  await checkPage(page, testInfo, "machine-terminal-opened");
  clean();
});
