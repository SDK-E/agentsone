// SPDX-License-Identifier: MIT
import { expect, type Page } from "@playwright/test";

/** Open a side panel by its tab name, from the home page, at phone or desktop width. */
export async function openPanel(page: Page, name: string): Promise<void> {
  await page.goto("/");
  if ((page.viewportSize()?.width ?? 0) < 768) {
    await page.getByRole("button", { name: "Open panels" }).click();
  }
  const tab = page.getByRole("tab", { name, exact: true });
  await tab.click();
  await expect(tab).toHaveAttribute("aria-selected", "true");
}
