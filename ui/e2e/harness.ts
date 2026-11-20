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
  ["GET", /^\/v1\/agents\/[^/]+$/, "getAgent"],
  ["POST", /^\/v1\/router\/route$/, "routeRequest"],
  ["GET", /^\/v1\/chart$/, "getChart"],
  ["GET", /^\/v1\/agents\/[^/]+\/chat$/, "listChatMessages"],
  ["POST", /^\/v1\/agents\/[^/]+\/chat$/, "sendChatMessage"],
  ["GET", /^\/v1\/agents\/[^/]+\/chat\/stream$/, "streamChat"],
  ["GET", /^\/v1\/agents\/[^/]+\/output$/, "streamAgentOutput"],
  ["GET", /^\/v1\/settings\/theme$/, "getThemeSelection"],
  ["PUT", /^\/v1\/settings\/theme$/, "setThemeSelection"],
  ["GET", /^\/v1\/themes\/[^/]+$/, "getTheme"],
  // monitor, tasks and audit
  ["GET", /^\/v1\/budget$/, "getBudget"],
  ["GET", /^\/v1\/budget\/split$/, "getBudgetSplit"],
  ["GET", /^\/v1\/machine\/metrics$/, "getMachineMetrics"],
  ["GET", /^\/v1\/tasks$/, "listTasks"],
  ["POST", /^\/v1\/tasks$/, "createTask"],
  ["GET", /^\/v1\/tasks\/stream$/, "streamTasks"],
  ["GET", /^\/v1\/tasks\/[^/]+$/, "getTask"],
  ["GET", /^\/v1\/schedules$/, "listSchedules"],
  ["GET", /^\/v1\/audit$/, "listAuditRecords"],
  ["POST", /^\/v1\/audit\/verify$/, "verifyAuditChain"],
  // ide
  ["GET", /^\/v1\/ide$/, "getIdeStatus"],
  ["GET", /^\/v1\/ide\/servers\/[^/]+$/, "getIdeServer"],
  ["PUT", /^\/v1\/ide\/servers\/[^/]+$/, "startIdeServer"],
  ["DELETE", /^\/v1\/ide\/servers\/[^/]+$/, "stopIdeServer"],
  // memory, plugins, settings
  ["GET", /^\/v1\/memory$/, "listMemory"],
  ["GET", /^\/v1\/memory\/search$/, "searchMemory"],
  ["GET", /^\/v1\/memory\/[^/]+$/, "getMemory"],
  ["DELETE", /^\/v1\/memory\/[^/]+$/, "deleteMemory"],
  ["GET", /^\/v1\/plugins$/, "listPlugins"],
  ["POST", /^\/v1\/plugins$/, "installPlugin"],
  ["DELETE", /^\/v1\/plugins\/[^/]+$/, "uninstallPlugin"],
  ["POST", /^\/v1\/plugins\/[^/]+\/enable$/, "enablePlugin"],
  ["POST", /^\/v1\/plugins\/[^/]+\/disable$/, "disablePlugin"],
  ["GET", /^\/v1\/plugin-sources$/, "listPluginSources"],
  ["GET", /^\/v1\/plugin-catalog$/, "searchPluginCatalog"],
  ["GET", /^\/v1\/humans$/, "listHumans"],
  ["POST", /^\/v1\/humans$/, "createHuman"],
  ["PATCH", /^\/v1\/humans\/[^/]+$/, "updateHuman"],
  ["DELETE", /^\/v1\/humans\/[^/]+$/, "deleteHuman"],
  ["GET", /^\/v1\/themes$/, "listThemes"],
  // machine
  ["GET", /^\/v1\/machine\/processes$/, "listProcesses"],
  ["GET", /^\/v1\/machine\/services$/, "listServices"],
  ["GET", /^\/v1\/machine\/packages$/, "listPackages"],
  ["GET", /^\/v1\/machine\/users$/, "listMachineUsers"],
  ["GET", /^\/v1\/machine\/files$/, "listFiles"],
  ["GET", /^\/v1\/machine\/files\/content$/, "readFile"],
  ["GET", /^\/v1\/machine\/pty$/, "listPtySessions"],
  ["POST", /^\/v1\/machine\/pty$/, "createPtySession"],
  ["DELETE", /^\/v1\/machine\/pty\/[^/]+$/, "closePtySession"],
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
    const wanted = operation ? (scenario.only?.[operation] ?? scenario.all ?? "") : "";
    const forcedError = wanted === "error" && scenario.only?.[operation ?? ""] === "error";
    // A stream with no fixture for this scenario (say "empty") must not replay the default
    // frames into it; refuse it so the interface falls back to polling the scenario's list.
    // An error scenario turns streams off too, so live frames do not paper over the error.
    const anyError = Object.values(scenario.only ?? {}).includes("error");
    const isStream = operation?.startsWith("stream") === true;
    const streamOff = isStream && (anyError || (wanted !== "" && !hasFixture(operation, wanted)));
    if (operation && (forcedError || streamOff)) {
      // The query form is strict: with no such fixture the fake daemon answers 404.
      url.searchParams.set("fixture", wanted === "" ? "error" : wanted);
      expectedFailures.add(url.pathname);
      await route.continue({ url: url.href });
      return;
    }
    // The header form falls back to the default fixture when the variant does not exist.
    if (operation && !hasFixture(operation, wanted) && !hasFixture(operation)) {
      expectedFailures.add(url.pathname); // no fixture at all yet: the interface polls instead
    }
    await route.continue({ headers: { ...request.headers(), "x-fixture-variant": wanted } });
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
