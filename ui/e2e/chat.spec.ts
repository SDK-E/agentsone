// SPDX-License-Identifier: MIT
import { expect, test } from "@playwright/test";

import { checkPage, STATES, useScenario, type Scenario } from "./harness";

const scenarios: Record<(typeof STATES)[number], Scenario> = {
  empty: { all: "empty" },
  loaded: {},
  error: { only: { listChatMessages: "error" } },
  "long-text": { all: "long-text" },
};

for (const state of STATES) {
  test(`chat, ${state}`, async ({ page }, testInfo) => {
    const clean = await useScenario(page, scenarios[state]);
    await page.goto("/agents/builder/chat");

    await expect(page.getByRole("textbox", { name: /^Message / })).toBeVisible();
    const messages = page.getByRole("region", { name: "Messages" });
    if (state === "empty") {
      await expect(messages.getByRole("heading", { name: "No messages yet" })).toBeVisible();
    } else if (state === "error") {
      await expect(messages.getByRole("alert")).toBeVisible();
    } else {
      await expect(messages.getByRole("listitem").first()).toBeVisible();
    }
    await checkPage(page, testInfo, `chat-${state}`);
    clean();
  });
}

test("chat, open from the society and send", async ({ page }, testInfo) => {
  const clean = await useScenario(page, {});
  await page.goto("/");

  await page.getByRole("link", { name: "Open chat with Keeper" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Keeper" })).toBeVisible();
  await page.getByRole("textbox", { name: "Message Keeper" }).fill("How full is the disk?");
  await page.getByRole("button", { name: "Send" }).click();

  await expect(page.getByRole("textbox", { name: "Message Keeper" })).toHaveValue("");
  await checkPage(page, testInfo, "chat-sent");
  clean();
});
