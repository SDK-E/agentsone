// SPDX-License-Identifier: MIT
import { act, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoAxeViolations } from "../../test/axe";
import { renderWith } from "../../test/fake-api";
import { desktopUrl, type VncConnector, type VncEvents } from "./connection";
import { DesktopView } from "./desktop-view";
import DesktopPanel from ".";

/** A VNC client that records what the panel asks of it; the test plays the server. */
function fakeVnc() {
  const calls = {
    urls: [] as string[],
    keys: [] as [number, string, boolean | undefined][],
    disconnects: 0,
    focus: 0,
  };
  let events: VncEvents | undefined;
  let fail = false;
  const connect: VncConnector = (_target, url, ev) => {
    calls.urls.push(url);
    events = ev;
    if (fail) return Promise.reject(new Error("no client"));
    return Promise.resolve({
      disconnect: () => {
        calls.disconnects++;
        ev.onDisconnect(true);
      },
      sendKey: (keysym, code, down) => calls.keys.push([keysym, code, down]),
      focus: () => calls.focus++,
    });
  };
  return {
    connect,
    calls,
    failNext: () => {
      fail = true;
    },
    serverConnects: () => {
      act(() => {
        events?.onConnect();
      });
    },
    serverDrops: (clean: boolean) => {
      act(() => {
        events?.onDisconnect(clean);
      });
    },
  };
}

const open = () => screen.getByRole("button", { name: "Open my desktop" });

describe("Desktop panel", () => {
  it("waits for the person before starting a desktop", async () => {
    const vnc = fakeVnc();
    renderWith(<DesktopView human="ada" connect={vnc.connect} />);
    expect(open()).toBeVisible();
    expect(screen.getByText(/as your own user/)).toBeVisible();
    expect(vnc.calls.urls).toEqual([]);
    await expectNoAxeViolations(document.body);
  });

  it("opens the person's own desktop and shows its tools once connected", async () => {
    const vnc = fakeVnc();
    renderWith(<DesktopView human="ada" connect={vnc.connect} />);
    await userEvent.click(open());
    expect(vnc.calls.urls).toEqual([desktopUrl(window.location, "ada")]);
    expect(await screen.findByRole("status")).toHaveTextContent("Starting your desktop");
    vnc.serverConnects();
    const tools = await screen.findByRole("toolbar", { name: "Desktop tools" });
    expect(tools).toBeVisible();
    expect(screen.getByRole("application", { name: "Your desktop" })).toBeVisible();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    await expectNoAxeViolations(document.body);
  });

  it("opens a terminal and a web browser inside the desktop", async () => {
    const vnc = fakeVnc();
    renderWith(<DesktopView human="ada" connect={vnc.connect} />);
    await userEvent.click(open());
    vnc.serverConnects();
    await userEvent.click(await screen.findByRole("button", { name: "Terminal" }));
    await userEvent.click(screen.getByRole("button", { name: "Web browser" }));
    const SUPER = 0xffeb;
    expect(vnc.calls.keys).toEqual([
      [SUPER, "MetaLeft", true],
      [0x74, "KeyT", undefined],
      [SUPER, "MetaLeft", false],
      [SUPER, "MetaLeft", true],
      [0x62, "KeyB", undefined],
      [SUPER, "MetaLeft", false],
    ]);
    expect(vnc.calls.focus).toBe(2);
  });

  it("says plainly when the desktop did not open, and tries again", async () => {
    const vnc = fakeVnc();
    renderWith(<DesktopView human="ada" connect={vnc.connect} />);
    await userEvent.click(open());
    vnc.serverDrops(false);
    expect(await screen.findByRole("alert")).toHaveTextContent("The desktop did not open");
    await userEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(vnc.calls.urls).toHaveLength(2);
    await expectNoAxeViolations(document.body);
  });

  it("says so when the client itself cannot start", async () => {
    const vnc = fakeVnc();
    vnc.failNext();
    renderWith(<DesktopView human="ada" connect={vnc.connect} />);
    await userEvent.click(open());
    expect(await screen.findByRole("alert")).toHaveTextContent("The desktop did not open");
  });

  it("offers to reconnect when the desktop ends on its own", async () => {
    const vnc = fakeVnc();
    renderWith(<DesktopView human="ada" connect={vnc.connect} />);
    await userEvent.click(open());
    vnc.serverConnects();
    vnc.serverDrops(true);
    expect(await screen.findByText("The desktop connection ended.")).toBeVisible();
    expect(screen.getByRole("button", { name: "Reconnect" })).toBeVisible();
  });

  it("closing goes back to the start, and leaving the panel lets go of the connection", async () => {
    const vnc = fakeVnc();
    const { unmount } = renderWith(<DesktopView human="ada" connect={vnc.connect} />);
    await userEvent.click(open());
    vnc.serverConnects();
    await userEvent.click(await screen.findByRole("button", { name: "Disconnect" }));
    expect(open()).toBeVisible();
    expect(screen.queryByText("The desktop connection ended.")).not.toBeInTheDocument();
    await userEvent.click(open());
    vnc.serverConnects();
    unmount();
    expect(vnc.calls.disconnects).toBe(2);
  });

  it("uses wss when the page is on https", () => {
    const at = (href: string) => new URL(href) as unknown as Location;
    expect(desktopUrl(at("https://agents.example.com/"), "ada")).toBe(
      "wss://agents.example.com/desktop/ada/vnc",
    );
    expect(desktopUrl(at("http://127.0.0.1:7780/"), "ada")).toBe(
      "ws://127.0.0.1:7780/desktop/ada/vnc",
    );
  });

  it("the panel opens the signed-in human's desktop", async () => {
    renderWith(<DesktopPanel />);
    expect(await screen.findByRole("button", { name: "Open my desktop" })).toBeVisible();
  });
});
