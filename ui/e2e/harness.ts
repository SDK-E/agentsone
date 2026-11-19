// SPDX-License-Identifier: MIT
//
// Test harness for the page runs. A scenario tells the fake daemon which fixture variant to
// serve for each operation (X-Fixture-Variant). Requests still go to the fake daemon; nothing
// is answered from the test itself.
import { existsSync } from "node:fs";
import { fileURLToPath } from "node:url";

import AxeBuilder from "@axe-core/playwright";
import { expect, type Page, type TestInfo } from "@playwright/test";

export const STATES = ["empty", "loaded", "error", "long-text"] as const;
export type PageState = (typeof STATES)[number];

const fixtures = fileURLToPath(new URL("../../contract/fixtures/api/", import.meta.url));

const OPERATIONS: [method: string, path: RegExp, operation: string][] = [
  ["GET", /^\/v1\/setup$/, "getSetupStatus"],
  ["POST", /^\/v1\/setup\/first-human$/, "createFirstHuman"],
  ["GET", /^\/v1\/setup\/engine-licence$/, "getEngineLicence"],
  ["POST", /^\/v1\/setup\/engine-licence$/, "acceptEngineLicence"],
  ["GET", /^\/v1\/setup\/engine-fetch$/, "getEngineFetchStatus"],
  ["POST", /^\/v1\/session$/, "createSession"],
  ["DELETE", /^\/v1\/session$/, "deleteSession"],
  ["GET", /^\/v1\/session\/me$/, "getMe"],
  ["GET", /^\/v1\/router\/live$/, "listLiveList"],
  ["GET", /^\/v1\/router\/live\/stream$/, "streamLiveList"],
  ["POST", /^\/v1\/router\/route$/, "routeRequest"],
  ["GET", /^\/v1\/chart$/, "getChart"],
  ["GET", /^\/v1\/agents\/[^/]+\/chat$/, "listChatMessages"],
  ["POST", /^\/v1\/agents\/[^/]+\/chat$/, "sendChatMessage"],
  ["GET", /^\/v1\/agents\/[^/]+\/chat\/stream$/, "streamChat"],
  ["GET", /^\/v1\/agents\/[^/]+\/output$/, "streamAgentOutput"],
  ["GET", /^\/v1\/settings\/theme$/, "getThemeSelection"],
  ["PUT", /^\/v1\/settings\/theme$/, "setThemeSelection"],
  ["GET", /^\/v1\/themes\/[^/]+$/, "getTheme"],
];

function operationOf(method: string, path: string): string | undefined {
  return OPERATIONS.find(([m, p]) => m === method && p.test(path))?.[2];
}

const hasFixture = (operation: string, variant = "") =>
  existsSync(`${fixtures}${operation}${variant === "" ? "" : `.${variant}`}.json`);

export interface Scenario {
  /** Use this variant for every operation that has it ("empty", "long-text"). */
  all?: string;
  /** Variant per operation. "error" with no such fixture makes the fake daemon answer 404. */
  only?: Record<string, string>;
}

/**
 * Route every API call through the scenario, and fail the test on any console error or warning
 * except the failed loads the scenario asks for.
 */
export async function useScenario(page: Page, scenario: Scenario): Promise<() => void> {
  const expectedFailures = new Set<string>();
  const problems: string[] = [];

  await page.route("**/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const operation = operationOf(request.method(), url.pathname);
    let variant = "";
    if (operation) {
      const wanted = scenario.only?.[operation] ?? scenario.all ?? "";
      if (scenario.only?.[operation] !== undefined && wanted === "error") variant = wanted;
      else if (wanted !== "" && hasFixture(operation, wanted)) variant = wanted;
      // Streams have no fixture until the contract adds one; the interface falls back to polling.
      if (variant === "error" || !hasFixture(operation, variant)) {
        expectedFailures.add(url.pathname);
      }
    }
    await route.continue({ headers: { ...request.headers(), "x-fixture-variant": variant } });
  });

  page.on("console", (message) => {
    if (message.type() !== "error" && message.type() !== "warning") return;
    const where = message.location().url;
    const path = where.startsWith("http") ? new URL(where).pathname : where;
    if (message.text().startsWith("Failed to load resource") && expectedFailures.has(path)) return;
    problems.push(`${message.type()}: ${message.text()} (${where})`);
  });
  page.on("pageerror", (error) => problems.push(`pageerror: ${error.message}`));

  return () => {
    expect(problems, "the browser console stays clean").toEqual([]);
  };
}

/** Check the page against WCAG 2.2 AA and keep a full-page screenshot for human review. */
export async function checkPage(page: Page, testInfo: TestInfo, name: string): Promise<void> {
  await page.evaluate(() => document.fonts.ready);
  const axe = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"])
    .analyze();
  expect(axe.violations, "no accessibility violations").toEqual([]);
  const path = `screenshots/${testInfo.project.name}/${name}.png`;
  await testInfo.attach(name, {
    body: await page.screenshot({ fullPage: true, path }),
    contentType: "image/png",
  });
}
