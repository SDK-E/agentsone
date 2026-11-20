// SPDX-License-Identifier: MIT
import { expect, test } from "@playwright/test";

import { checkPage, STATES, useScenario, type Scenario } from "./harness";
import { openPanel } from "./open-panel";

const scenarios: Record<(typeof STATES)[number], Scenario> = {
  empty: { all: "empty" },
  loaded: {},
  error: { only: { listHumans: "error", listThemes: "error" } },
  "long-text": { all: "long-text" },
};

for (const state of STATES) {
  test(`settings, ${state}`, async ({ page }, testInfo) => {
    const clean = await useScenario(page, scenarios[state]);
    await openPanel(page, "Settings");

    await expect(page.getByRole("heading", { name: "Your account" })).toBeVisible();
    if (state === "error") {
      await expect(page.getByRole("alert").first()).toBeVisible();
    } else {
      await expect(page.getByRole("heading", { name: "People" })).toBeVisible();
    }
    await checkPage(page, testInfo, `settings-${state}`);
    clean();
  });
}

test("settings, add a person asks for each field", async ({ page }, testInfo) => {
  const clean = await useScenario(page, {});
  await openPanel(page, "Settings");

  await page.getByRole("button", { name: "Add person" }).click();

  await expect(page.getByText("The name must start with a letter", { exact: false })).toBeVisible();
  await checkPage(page, testInfo, "settings-add-person-rules");
  clean();
});

test("settings, remove a person asks first", async ({ page }, testInfo) => {
  const clean = await useScenario(page, {});
  await openPanel(page, "Settings");

  await page
    .getByRole("button", { name: /^Remove / })
    .first()
    .click();

  await expect(page.getByRole("button", { name: "Keep them" })).toBeVisible();
  await checkPage(page, testInfo, "settings-remove-person-confirm");
  clean();
});

test("settings, password change asks for the current password", async ({ page }, testInfo) => {
  const clean = await useScenario(page, {});
  await openPanel(page, "Settings");

  await page.getByRole("button", { name: "Change password" }).click();

  await expect(page.getByLabel("Current password")).toBeVisible();
  await expect(page.getByText("Type your current password.")).toBeVisible();
  // Start at the first label so it, all three fields, the errors and the button share the screen.
  await page.locator("label", { hasText: "Current password" }).evaluate((el) => {
    el.scrollIntoView({ block: "start" });
  });
  await page.getByRole("tabpanel", { name: "Settings" }).evaluate((el) => {
    el.scrollTop -= 12;
  });
  await checkPage(page, testInfo, "settings-password-rules");
  // Only what the phone or desktop screen shows, for a human look at the form.
  await page.screenshot({
    path: `screenshots/${testInfo.project.name}/settings-password-viewport.png`,
  });
  clean();
});

test("settings, turn on the Claude engine", async ({ page }, testInfo) => {
  const clean = await useScenario(page, { only: { getEngineLicence: "not-accepted" } });
  await openPanel(page, "Settings");

  await expect(page.getByText("The Claude engine is off.", { exact: false })).toBeVisible();
  await expect(page.getByRole("button", { name: "Accept and download" })).toHaveCount(0);
  await page.getByRole("button", { name: "Turn on the Claude engine" }).click();

  await expect(page.getByRole("region", { name: "Claude engine licence text" })).toBeVisible();
  await expect(
    page.getByRole("link", { name: /Anthropic's legal and compliance terms/ }),
  ).toHaveAttribute("href", "https://code.claude.com/docs/en/legal-and-compliance");
  await page.getByRole("button", { name: "Accept and download" }).scrollIntoViewIfNeeded();
  await checkPage(page, testInfo, "settings-engine-turn-on");
  // Only what the phone or desktop screen shows, for a human look at the opt-in.
  await page.screenshot({
    path: `screenshots/${testInfo.project.name}/settings-engine-turn-on-viewport.png`,
  });
  clean();
});

test("settings, read the licence", async ({ page }, testInfo) => {
  const clean = await useScenario(page, {});
  await openPanel(page, "Settings");

  await page.getByText("Read the licence").click();

  await expect(page.locator(".disclosure__body")).toBeVisible();
  await checkPage(page, testInfo, "settings-licence-open");
  clean();
});
