// SPDX-License-Identifier: MIT
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoAxeViolations } from "../../test/axe";
import { FakeApi, fixtures, problem, renderWith } from "../../test/fake-api";
import IdePanel from ".";

const stopped = { ...fixtures.ideStarting, state: "stopped" as const };

describe("IDE panel", () => {
  // axe-core in jsdom cannot step into frames; the Playwright run checks this state with axe.
  it("embeds the running editor and offers it in a new tab", async () => {
    const { api } = renderWith(<IdePanel />);

    const frame = await screen.findByTitle("Code editor for ada");
    expect(frame.tagName).toBe("IFRAME");
    expect(frame).toHaveAttribute("src", "/ide/ada/");
    // First-party and same origin through the daemon: the editor needs scripts, workers and storage.
    expect(frame).not.toHaveAttribute("sandbox");
    const tab = screen.getByRole("link", { name: "Open in a new tab" });
    expect(tab).toHaveAttribute("href", "/ide/ada/");
    expect(tab).toHaveAttribute("target", "_blank");
    expect(tab).toHaveAttribute("rel", "noopener noreferrer");
    expect(
      await screen.findByText(
        "Open-source VS Code (code-oss 1.106.0). It runs as your own Linux user.",
      ),
    ).toBeVisible();
    expect(api.calls).toContain("getIdeServer ada");
  });

  it("starts a stopped editor, says it is starting, then opens it", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.ideServer = stopped;
    const { container } = renderWith(<IdePanel />, api);

    expect(
      await screen.findByRole("heading", { name: "Your editor is not running" }),
    ).toBeVisible();
    await expectNoAxeViolations(container);
    await user.click(screen.getByRole("button", { name: "Start the editor" }));
    expect(api.calls).toContain("startIdeServer ada");
    expect(await screen.findByRole("status")).toHaveTextContent("Starting your editor…");
    expect(await screen.findByTitle("Code editor for ada", undefined, { timeout: 3000 })).toBe(
      screen.getByTitle("Code editor for ada"),
    );
  });

  it("says why the editor failed and lets the person try again", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.ideServer = { ...stopped, state: "failed", error: "settings.json is not valid JSON." };
    renderWith(<IdePanel />, api);

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Your editor could not start.");
    expect(alert).toHaveTextContent("The machine said: settings.json is not valid JSON.");
    await user.click(within(alert).getByRole("button", { name: "Try again" }));
    expect(api.calls).toContain("startIdeServer ada");
  });

  it("says when starting is refused", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.ideServer = stopped;
    renderWith(<IdePanel />, api);

    api.mutationError = problem(403, "Forbidden", "Only ada may start this editor.");
    await user.click(await screen.findByRole("button", { name: "Start the editor" }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("The editor could not start.");
    expect(alert).toHaveTextContent("Only ada may start this editor.");
  });

  it("stops the editor", async () => {
    const user = userEvent.setup();
    const { api } = renderWith(<IdePanel />);

    await user.click(await screen.findByRole("button", { name: "Stop the editor" }));
    expect(api.calls).toContain("stopIdeServer ada");
    expect(
      await screen.findByRole("heading", { name: "Your editor is not running" }),
    ).toBeVisible();
  });

  it("says when stopping fails", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    renderWith(<IdePanel />, api);

    api.mutationError = problem(503, "Unavailable");
    await user.click(await screen.findByRole("button", { name: "Stop the editor" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("The editor could not stop.");
  });

  it("explains a failed load and offers to retry", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.ideServer = problem(503, "Unavailable", "The IDE service is starting.");
    renderWith(<IdePanel />, api);

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("The IDE service is starting.");
    api.ideServer = stopped;
    await user.click(within(alert).getByRole("button", { name: "Try again" }));
    expect(
      await screen.findByRole("heading", { name: "Your editor is not running" }),
    ).toBeVisible();
  });

  it("explains when it cannot tell who is signed in", async () => {
    const api = new FakeApi();
    api.me = problem(503, "Unavailable");
    renderWith(<IdePanel />, api);

    expect(await screen.findByRole("alert")).toBeVisible();
  });

  it("tells an agent that the editor is for people", async () => {
    const api = new FakeApi();
    api.me = { kind: "agent", agent: "builder", scopes: [] } as unknown as typeof api.me;
    renderWith(<IdePanel />, api);

    expect(
      await screen.findByRole("heading", { name: "Only people have an editor here" }),
    ).toBeVisible();
  });
});
