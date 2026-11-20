// SPDX-License-Identifier: MIT
//
// The Desktop panel: the start card, a connected desktop and a desktop that did not open, in
// light and dark, at phone and desktop width. A small fake VNC server stands in for the daemon's
// desktop bridge (the fake daemon has no desktop): it greets, offers no authentication and
// describes a 1280 x 800 screen, which is all noVNC needs to show itself connected.
import { expect, test, type Page, type WebSocketRoute } from "@playwright/test";

import { checkPage, useScenario } from "./harness";
import { openPanel } from "./open-panel";

const BRIDGE = /\/desktop\/ada\/vnc$/;

/** ServerInit: a 1280 x 800 screen in 32-bit true colour, named "ada's desktop". */
function serverInit(): Buffer {
  const name = Buffer.from("ada's desktop");
  const b = Buffer.alloc(24 + name.length);
  b.writeUInt16BE(1280, 0);
  b.writeUInt16BE(800, 2);
  b.writeUInt8(32, 4); // bits per pixel
  b.writeUInt8(24, 5); // depth
  b.writeUInt8(0, 6); // little endian
  b.writeUInt8(1, 7); // true colour
  b.writeUInt16BE(255, 8);
  b.writeUInt16BE(255, 10);
  b.writeUInt16BE(255, 12);
  b.writeUInt8(16, 14);
  b.writeUInt8(8, 15);
  b.writeUInt8(0, 16);
  b.writeUInt32BE(name.length, 20);
  name.copy(b, 24);
  return b;
}

/** Plays the VNC server's side of the handshake: RFB 3.8, security type None. */
function vncServer(ws: WebSocketRoute): void {
  let step = 0;
  ws.send(Buffer.from("RFB 003.008\n"));
  ws.onMessage(() => {
    step++;
    if (step === 1) ws.send(Buffer.from([1, 1])); // one security type: None
    if (step === 2) ws.send(Buffer.from([0, 0, 0, 0])); // security result: OK
    if (step === 3) ws.send(serverInit()); // after ClientInit
  });
}

async function openDesktopPanel(page: Page) {
  await openPanel(page, "Desktop");
  await noVncSeesTheMock(page);
  return page.getByRole("tabpanel", { name: "Desktop" });
}

/**
 * noVNC checks that its WebSocket has send, onopen and the rest on the object or its own
 * prototype. Playwright's WebSocket mock keeps them one prototype further up, so the check
 * fails before any byte moves. A thin subclass lists them where noVNC looks. Test code only:
 * real browsers' WebSocket passes the check.
 */
async function noVncSeesTheMock(page: Page): Promise<void> {
  await page.evaluate(() => {
    const Mock = window.WebSocket;
    class Listed extends Mock {}
    const names = ["send", "close", "binaryType", "onerror", "onmessage", "onopen", "onclose"];
    for (const name of [...names, "protocol", "readyState"]) {
      for (
        let p: object | null = Mock.prototype;
        p;
        p = Object.getPrototypeOf(p) as object | null
      ) {
        const d = Object.getOwnPropertyDescriptor(p, name);
        if (d) {
          Object.defineProperty(Listed.prototype, name, d);
          break;
        }
      }
    }
    window.WebSocket = Listed;
  });
}

test("desktop, before it opens", async ({ page }, testInfo) => {
  const clean = await useScenario(page, {});
  let opened = false;
  await page.routeWebSocket(BRIDGE, (ws) => {
    opened = true;
    vncServer(ws);
  });
  const panel = await openDesktopPanel(page);
  await expect(panel.getByRole("heading", { name: "Your desktop" })).toBeVisible();
  await expect(panel.getByRole("button", { name: "Open my desktop" })).toBeVisible();
  expect(opened, "no desktop starts before the person asks").toBe(false);
  await checkPage(page, testInfo, "desktop-start");
  clean();
});

test("desktop, connected", async ({ page }, testInfo) => {
  const clean = await useScenario(page, {});
  await page.routeWebSocket(BRIDGE, vncServer);
  const panel = await openDesktopPanel(page);
  await panel.getByRole("button", { name: "Open my desktop" }).click();
  const tools = panel.getByRole("toolbar", { name: "Desktop tools" });
  await expect(tools).toBeVisible();
  await expect(tools.getByRole("button", { name: "Terminal" })).toBeVisible();
  await expect(tools.getByRole("button", { name: "Web browser" })).toBeVisible();
  const screen = panel.getByRole("application", { name: "Your desktop" });
  await expect(screen).toBeVisible();
  await expect(screen.locator("canvas")).toBeVisible();
  await checkPage(page, testInfo, "desktop-connected");
  await tools.getByRole("button", { name: "Disconnect" }).click();
  await expect(panel.getByRole("button", { name: "Open my desktop" })).toBeVisible();
  clean();
});

test("desktop, did not open", async ({ page }, testInfo) => {
  const clean = await useScenario(page, {});
  await page.routeWebSocket(BRIDGE, (ws) => {
    void ws.close({ code: 1011, reason: "the desktop did not start" });
  });
  const panel = await openDesktopPanel(page);
  await panel.getByRole("button", { name: "Open my desktop" }).click();
  await expect(panel.getByRole("alert")).toHaveText(
    "The desktop did not open. Try again in a moment.",
  );
  await expect(panel.getByRole("button", { name: "Try again" })).toBeVisible();
  await checkPage(page, testInfo, "desktop-error");
  clean();
});
