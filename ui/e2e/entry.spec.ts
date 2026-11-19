// SPDX-License-Identifier: MIT
import { expect, test } from "@playwright/test";

import { checkPage, STATES, useScenario, type Scenario } from "./harness";

const LONG =
  "a-very-long-human-name-that-keeps-going-and-going-to-test-wrapping-in-every-field-of-the-form";

const setupScenarios: Record<(typeof STATES)[number], Scenario> = {
  empty: { only: { getSetupStatus: "needs-human" } },
  loaded: { only: { getSetupStatus: "needs-licence" } },
  error: { only: { getSetupStatus: "error" } },
  "long-text": { only: { getSetupStatus: "fetching-engine" } },
};

for (const state of STATES) {
  test(`setup, ${state}`, async ({ page }, testInfo) => {
    const clean = await useScenario(page, setupScenarios[state]);
    await page.goto("/setup");

    await expect(
      page.getByRole("heading", { level: 1, name: "Welcome to Agents One" }),
    ).toBeVisible();
    if (state === "error") await expect(page.getByRole("alert")).toBeVisible();
    else await expect(page.getByRole("heading", { level: 2 })).toBeVisible();
    await checkPage(page, testInfo, `setup-${state}`);
    clean();
  });
}

test("setup, licence accepted starts the download", async ({ page }, testInfo) => {
  const clean = await useScenario(page, { only: { getSetupStatus: "needs-licence" } });
  await page.goto("/setup");

  await expect(page.getByRole("article", { name: "Claude engine licence text" })).toBeVisible();
  await expect(page.getByText("Nothing downloads before you accept.")).toBeVisible();
  await checkPage(page, testInfo, "setup-licence");
  clean();
});

const signInScenarios: Record<(typeof STATES)[number], Scenario> = {
  empty: {},
  loaded: {},
  error: { only: { createSession: "error" } },
  "long-text": {},
};

for (const state of STATES) {
  test(`sign in, ${state}`, async ({ page }, testInfo) => {
    const clean = await useScenario(page, signInScenarios[state]);
    await page.goto("/sign-in");

    await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
    if (state !== "empty") {
      await page.getByRole("textbox", { name: "Name" }).fill(state === "long-text" ? LONG : "ada");
      await page.getByLabel("Password").fill("correct horse battery staple");
    }
    if (state === "error") {
      await page.getByRole("button", { name: "Sign in" }).click();
      await expect(page.getByRole("alert")).toBeVisible();
    }
    await checkPage(page, testInfo, `sign-in-${state}`);
    clean();
  });
}

test("sign in, then home", async ({ page }) => {
  const clean = await useScenario(page, {});
  await page.goto("/sign-in");

  await page.getByRole("textbox", { name: "Name" }).fill("ada");
  await page.getByLabel("Password").fill("correct horse battery staple");
  await page.getByRole("button", { name: "Sign in" }).click();

  await expect(page.getByRole("heading", { name: "Ask the society" })).toBeVisible();
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page).toHaveURL(/\/sign-in$/);
  clean();
});
