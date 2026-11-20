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
      await expect(panel.getByRole("list", { name: "Records, newest first" })).toBeVisible();
      await expect(panel.getByText("Proof").first()).toBeVisible();
      await expect(panel.getByText("Testimony").first()).toBeVisible();
    }
    await checkPage(page, testInfo, `audit-${state}`);
    clean();
  });
}

test("panel tabs, a cue shows where more tabs hide and the chosen tab comes into view", async ({
  page,
}, testInfo) => {
  test.skip((page.viewportSize()?.width ?? 0) >= 768, "the tabs only overflow on a phone");
  const clean = await useScenario(page, {});
  await openPanel(page, "Monitor");

  const scroller = page.locator(".tabs__scroller");
  await expect(scroller).toHaveAttribute("data-more-end", "true");
  await checkPage(page, testInfo, "panel-tabs-cue");

  await page.getByRole("tab", { name: "Audit" }).click();
  await expect(page.getByRole("tab", { name: "Audit" })).toBeInViewport({ ratio: 1 });
  await page.getByRole("tab", { name: "Settings" }).click();
  await expect(page.getByRole("tab", { name: "Settings" })).toBeInViewport({ ratio: 1 });
  await expect(scroller).not.toHaveAttribute("data-more-end");
  await expect(scroller).toHaveAttribute("data-more-start", "true");
  clean();
});

test("panel tabs, a tab chosen with the keyboard is whole and clear of the cues", async ({
  page,
}) => {
  test.skip((page.viewportSize()?.width ?? 0) >= 768, "the tabs only overflow on a phone");
  const clean = await useScenario(page, {});
  await openPanel(page, "Monitor");

  const scroller = page.locator(".tabs__scroller");
  const names = await page.getByRole("tab").allInnerTexts();
  expect(names.length).toBeGreaterThan(5);

  // The page does not scroll the tab first (as a click does): the app alone brings it into view.
  await page.getByRole("tab", { name: "Monitor" }).focus();
  await page.keyboard.press("End");
  const last = page.getByRole("tab", { name: names[names.length - 1] ?? "", exact: true });
  await expect(last).toHaveAttribute("aria-selected", "true");
  await expect(last).toBeInViewport({ ratio: 1 });
  // Every tab is in view at the end of the row, so no cue is left saying more hides there.
  await expect(scroller).not.toHaveAttribute("data-more-end");
  await expect(scroller).toHaveAttribute("data-more-start", "true");

  // Walking back, each chosen tab sits whole, not under the fade of a cue that is showing.
  await page.keyboard.press("Home");
  for (const name of names) {
    const tab = page.getByRole("tab", { name, exact: true });
    await expect(tab).toHaveAttribute("aria-selected", "true");
    await expect
      .poll(async () =>
        tab.evaluate((el) => {
          const row = el.parentElement?.getBoundingClientRect();
          const box = el.getBoundingClientRect();
          const scrollerEl = el.closest(".tabs__scroller");
          if (!row || !scrollerEl) return false;
          // The focus ring reaches 5px past the tab; it must clear the whole cue, fade included.
          const ring = 5;
          const cueBox = (side: "start" | "end") =>
            scrollerEl.hasAttribute(`data-more-${side}`)
              ? scrollerEl.querySelector(`.tabs__cue--${side}`)?.getBoundingClientRect()
              : undefined;
          const start = cueBox("start");
          const end = cueBox("end");
          // With no cue on a side the tab need only be whole there.
          return (
            (start ? box.left - ring >= start.right - 0.5 : box.left >= row.left - 0.5) &&
            (end ? box.right + ring <= end.left + 0.5 : box.right <= row.right + 0.5)
          );
        }),
      )
      .toBe(true);
    await page.keyboard.press("ArrowRight");
  }
  clean();
});

test("panel tabs, at rest no tab shows a part of its label outside the cues", async ({ page }) => {
  test.skip((page.viewportSize()?.width ?? 0) >= 768, "the tabs only overflow on a phone");
  const clean = await useScenario(page, {});
  await openPanel(page, "Monitor");
  const scroller = page.locator(".tabs__scroller");
  const list = page.getByRole("tablist");

  // A tab cut by an edge may show only under that edge's solid cue (and its fade over the tab's
  // own padding, which is empty): never a part of the label.
  const cutTabs = () =>
    scroller.evaluate((el) => {
      const row = el.querySelector(".tabs__list")?.getBoundingClientRect();
      if (!row) return ["no row"];
      // How far a side's cue is solid, and how far it reaches with its fade; none when not shown.
      const cue = (side: string) => {
        const node = el.querySelector(`.tabs__cue--${side}`);
        if (!node || !el.hasAttribute(`data-more-${side}`)) return { solid: 0, reach: 0 };
        const style = getComputedStyle(node);
        const solid = parseFloat(style.getPropertyValue("--cue-solid"));
        return { solid, reach: solid + parseFloat(style.getPropertyValue("--cue-fade")) };
      };
      const start = cue("start");
      const end = cue("end");
      const problems: string[] = [];
      for (const tab of el.querySelectorAll('[role="tab"]')) {
        const box = tab.getBoundingClientRect();
        // The label ends 16px before the tab does, and starts 16px after it begins.
        const from = box.left + 16;
        const to = box.right - 16;
        // At each side the label is wholly under the solid cue, or wholly beyond the cue.
        const startOk = to <= row.left + start.solid + 0.5 || from >= row.left + start.reach - 0.5;
        const endOk = from >= row.right - end.solid - 0.5 || to <= row.right - end.reach + 0.5;
        if (!startOk || !endOk) problems.push(`${tab.textContent} ${startOk ? "end" : "start"}`);
      }
      return problems;
    });
  const atRest = async () => {
    // Let a scroll, and the snap after it, finish.
    await list.evaluate(
      (el) =>
        new Promise<void>((done) => {
          let last = el.scrollLeft;
          let still = 0;
          const tick = () => {
            still = el.scrollLeft === last ? still + 1 : 0;
            last = el.scrollLeft;
            if (still >= 8) done();
            else requestAnimationFrame(tick);
          };
          requestAnimationFrame(tick);
        }),
    );
    expect(await cutTabs()).toEqual([]);
  };

  await atRest();
  // A click, and the keyboard, to each end and back.
  for (const name of ["Plugins", "Audit", "Settings", "Desktop", "Tasks"]) {
    await page.getByRole("tab", { name, exact: true }).click();
    await atRest();
  }
  await page.getByRole("tab", { name: "Tasks", exact: true }).focus();
  await page.keyboard.press("End");
  await atRest();
  for (let i = 0; i < 9; i++) {
    await page.keyboard.press("ArrowLeft");
    await atRest();
  }
  // A swipe that ends anywhere along the row.
  const room = await list.evaluate((el) => el.scrollWidth - el.clientWidth);
  for (let at = 0; at <= room; at += 37) {
    await list.evaluate((el, left) => {
      el.scrollLeft = left;
    }, at);
    await atRest();
  }
  clean();
});

test("panel tabs, the arrows on both sides sit on a solid fade, clear of tab text", async ({
  page,
}, testInfo) => {
  test.skip((page.viewportSize()?.width ?? 0) >= 768, "the tabs only overflow on a phone");
  const clean = await useScenario(page, {});
  await openPanel(page, "Monitor");
  // Half way along the row, so tabs hide on both sides.
  await page.getByRole("tablist").evaluate((el) => {
    el.scrollLeft = (el.scrollWidth - el.clientWidth) / 2;
  });

  const scroller = page.locator(".tabs__scroller");
  await expect(scroller).toHaveAttribute("data-more-start", "true");
  await expect(scroller).toHaveAttribute("data-more-end", "true");

  for (const side of ["start", "end"] as const) {
    const cue = page.locator(`.tabs__cue--${side}`);
    const { solid, reach } = await cue.evaluate((el, edge) => {
      const arrow = el.querySelector("svg")?.getBoundingClientRect();
      const box = el.getBoundingClientRect();
      return {
        solid: parseFloat(getComputedStyle(el).getPropertyValue("--cue-solid")),
        // How far from its own edge the arrow reaches into the row.
        reach: arrow ? (edge === "start" ? arrow.right - box.left : box.right - arrow.left) : NaN,
      };
    }, side);
    expect(reach, `the ${side} arrow is drawn`).toBeGreaterThan(0);
    expect(reach, `the ${side} arrow stays on the solid part of its fade`).toBeLessThanOrEqual(
      solid,
    );
    expect(await cue.evaluate((el) => getComputedStyle(el).backgroundImage)).toContain(
      `${String(solid)}px`,
    );
  }
  await page.screenshot({
    path: `screenshots/${testInfo.project.name}/panel-tabs-both-cues-viewport.png`,
  });
  clean();
});

test("panels sheet, it reaches the screen bottom, keeps its header in view and holds the page still", async ({
  page,
}) => {
  test.skip((page.viewportSize()?.width ?? 0) >= 768, "the sheet is for phones");
  const clean = await useScenario(page, {});
  await page.goto("/");
  await page.getByRole("button", { name: "Open panels" }).click();
  const sheet = page.getByRole("dialog", { name: "Panels" });
  await expect(sheet).toBeVisible();

  const viewport = page.viewportSize();
  const box = await sheet.boundingBox();
  expect(box?.y).toBeGreaterThan(0);
  expect((box?.y ?? 0) + (box?.height ?? 0)).toBeCloseTo(viewport?.height ?? 0, 0);

  const before = await page.evaluate(() => window.scrollY);
  const panel = sheet.getByRole("tabpanel", { name: "Monitor" });
  await panel.hover();
  await page.mouse.wheel(0, 4000);
  await expect
    .poll(() => panel.evaluate((el) => el.scrollTop + el.clientHeight >= el.scrollHeight - 1))
    .toBe(true);
  expect(await page.evaluate(() => window.scrollY)).toBe(before);

  await expect(sheet.getByRole("heading", { name: "Panels" })).toBeInViewport({ ratio: 1 });
  await expect(sheet.getByRole("tab", { name: "Monitor" })).toBeInViewport({ ratio: 1 });
  await expect(sheet.getByRole("button", { name: "Close" })).toBeInViewport({ ratio: 1 });
  await sheet.getByRole("button", { name: "Close" }).click();
  await expect(sheet).toBeHidden();
  clean();
});

test("audit, show only proof", async ({ page }, testInfo) => {
  const clean = await useScenario(page, {});
  await openPanel(page, "Audit");

  const panel = page.getByRole("tabpanel", { name: "Audit" });
  await expect(panel.getByRole("list", { name: "Records, newest first" })).toBeVisible();
  await panel.getByRole("group", { name: /Show/ }).getByRole("button", { name: "Proof" }).click();
  await expect(panel.getByText("Told by the machine")).toHaveCount(0);
  await checkPage(page, testInfo, "audit-proof-only");
  clean();
});
