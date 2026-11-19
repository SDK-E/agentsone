// SPDX-License-Identifier: MIT
//
// The Monitor, Tasks and Audit panels, in every state, in light and dark, at phone and desktop
// width. The fake daemon serves the contract's golden fixtures.
import { expect, test, type Page } from "@playwright/test";

import { checkPage, STATES, useScenario, type PageState, type Scenario } from "./harness";

async function openPanel(page: Page, name: string): Promise<void> {
  await page.goto("/");
  if ((page.viewportSize()?.width ?? 0) < 768) {
    await page.getByRole("button", { name: "Open panels" }).click();
  }
  await page.getByRole("tab", { name }).click();
}

const monitor: Record<PageState, Scenario> = {
  empty: { all: "empty" },
  loaded: {},
  error: { only: { getBudget: "error", getMachineMetrics: "error", listLiveList: "error" } },
  "long-text": { all: "long-text" },
};

for (const state of STATES) {
  test(`monitor, ${state}`, async ({ page }, testInfo) => {
    const clean = await useScenario(page, monitor[state]);
    await openPanel(page, "Monitor");

    const panel = page.getByRole("tabpanel", { name: "Monitor" });
    if (state === "error") {
      await expect(panel.getByRole("alert").first()).toBeVisible();
    } else if (state === "empty") {
      await expect(panel.getByRole("heading", { name: "No agents yet" })).toBeVisible();
    } else {
      await expect(panel.getByText("Hard limit", { exact: true })).toBeVisible();
      if (state === "loaded") {
        await expect(panel.getByText("Soft limit", { exact: true }).first()).toBeVisible();
      }
      await expect(panel.getByRole("list", { name: "Agents, most recent first" })).toBeVisible();
    }
    await checkPage(page, testInfo, `monitor-${state}`);
    clean();
  });
}

test("monitor, budget used up", async ({ page }, testInfo) => {
  const clean = await useScenario(page, { only: { getBudget: "exhausted" } });
  await openPanel(page, "Monitor");

  const panel = page.getByRole("tabpanel", { name: "Monitor" });
  await expect(panel.getByRole("alert")).toBeVisible();
  await checkPage(page, testInfo, "monitor-exhausted");
  clean();
});

const tasks: Record<PageState, Scenario> = {
  empty: { all: "empty" },
  loaded: {},
  error: { only: { listTasks: "error" } },
  "long-text": { all: "long-text" },
};

for (const state of STATES) {
  test(`tasks, ${state}`, async ({ page }, testInfo) => {
    const clean = await useScenario(page, tasks[state]);
    await openPanel(page, "Tasks");

    const panel = page.getByRole("tabpanel", { name: "Tasks" });
    if (state === "error") {
      await expect(panel.getByRole("alert")).toBeVisible();
    } else if (state === "empty") {
      await expect(panel.getByRole("heading", { name: "No tasks yet" })).toBeVisible();
    } else {
      await expect(panel.getByRole("list", { name: "Tasks, newest first" })).toBeVisible();
    }
    await checkPage(page, testInfo, `tasks-${state}`);
    clean();
  });
}

test("tasks, one task in full", async ({ page }, testInfo) => {
  const clean = await useScenario(page, {});
  await openPanel(page, "Tasks");

  const panel = page.getByRole("tabpanel", { name: "Tasks" });
  await panel
    .getByRole("list", { name: "Tasks, newest first" })
    .getByRole("button")
    .first()
    .click();
  await expect(panel.getByRole("button", { name: /Back/ })).toBeVisible();
  await checkPage(page, testInfo, "tasks-detail");
  clean();
});

test("tasks, make a task", async ({ page }, testInfo) => {
  const clean = await useScenario(page, {});
  await openPanel(page, "Tasks");

  const panel = page.getByRole("tabpanel", { name: "Tasks" });
  await panel.getByRole("button", { name: "New task" }).click();
  await panel.getByRole("button", { name: "Create task" }).click();
  await expect(panel.getByText("Say what needs doing.")).toBeVisible();
  await checkPage(page, testInfo, "tasks-form-rule");

  await panel.getByRole("textbox", { name: "What needs doing" }).fill("Write the release notes");
  await panel.getByRole("button", { name: "Create task" }).click();
  await expect(panel.getByText("Task created.")).toBeVisible();
  clean();
});

for (const state of ["empty", "loaded"] as const) {
  test(`tasks, schedules, ${state}`, async ({ page }, testInfo) => {
    const clean = await useScenario(page, state === "empty" ? { all: "empty" } : {});
    await openPanel(page, "Tasks");

    const panel = page.getByRole("tabpanel", { name: "Tasks" });
    await panel.getByRole("button", { name: "Schedules" }).click();
    if (state === "empty") {
      await expect(panel.getByRole("heading", { name: "No schedules yet" })).toBeVisible();
    } else {
      await expect(panel.getByRole("list").first()).toBeVisible();
    }
    await checkPage(page, testInfo, `schedules-${state}`);
    clean();
  });
}

const audit: Record<PageState, Scenario> = {
  empty: { all: "empty" },
  loaded: {},
  error: { only: { listAuditRecords: "error", verifyAuditChain: "error" } },
  "long-text": { all: "long-text" },
};

for (const state of STATES) {
  test(`audit, ${state}`, async ({ page }, testInfo) => {
    const clean = await useScenario(page, audit[state]);
    await openPanel(page, "Audit");

    const panel = page.getByRole("tabpanel", { name: "Audit" });
    if (state === "error") {
      await expect(panel.getByRole("alert").first()).toBeVisible();
    } else if (state === "empty") {
      await expect(panel.getByRole("heading", { name: "No records yet" })).toBeVisible();
    } else {
      await expect(panel.getByText("The chain is intact.")).toBeVisible();
      await expect(panel.getByRole("list", { name: "Records, oldest first" })).toBeVisible();
      await expect(panel.getByText("Proof").first()).toBeVisible();
      await expect(panel.getByText("Testimony").first()).toBeVisible();
    }
    await checkPage(page, testInfo, `audit-${state}`);
    clean();
  });
}

test("audit, show only proof", async ({ page }, testInfo) => {
  const clean = await useScenario(page, {});
  await openPanel(page, "Audit");

  const panel = page.getByRole("tabpanel", { name: "Audit" });
  await expect(panel.getByRole("list", { name: "Records, oldest first" })).toBeVisible();
  await panel.getByRole("group", { name: /Show/ }).getByRole("button", { name: "Proof" }).click();
  await expect(panel.getByText("Told by the machine")).toHaveCount(0);
  await checkPage(page, testInfo, "audit-proof-only");
  clean();
});
