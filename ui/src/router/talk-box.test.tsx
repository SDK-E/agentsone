// SPDX-License-Identifier: MIT
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoAxeViolations } from "../test/axe";
import { FakeApi, fixtures, renderApp } from "../test/fake-api";

const box = () => screen.findByRole("textbox", { name: "What do you need?" });

describe("TalkBox", () => {
  it("routes the request and opens the chosen agent's chat with the reasons", async () => {
    const user = userEvent.setup();
    const { api } = renderApp("/");

    await user.type(await box(), "Add a budget page{Enter}");

    expect(api.calls).toContain("routeRequest Add a budget page");
    expect(await screen.findByRole("heading", { name: "Why Builder" })).toBeVisible();
    expect(screen.getByText(/Owns the docs site goal/)).toBeVisible();
    expect(screen.getByText("2 other agents were considered")).toBeVisible();
    expect(await screen.findByRole("link", { name: "Scribe" })).toHaveAttribute(
      "href",
      "/agents/scribe/chat",
    );
  });

  it("shows router hand-offs when a router passed the request on", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.route = {
      ...fixtures.route,
      hops: [{ router: "router", handed_to: "docs-router", reason: "Docs requests go there." }],
    };
    renderApp("/", api);

    await user.type(await box(), "Docs please");
    await user.click(screen.getByRole("button", { name: "Ask" }));

    expect(
      await screen.findByText("Router handed it to docs-router: Docs requests go there."),
    ).toBeVisible();
  });

  it("keeps the Ask button off until there is something to ask", async () => {
    renderApp("/");

    await box();
    expect(screen.getByRole("button", { name: "Ask" })).toBeDisabled();
  });

  it("explains a routing failure in the daemon's words", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.route = fixtures.routeError;
    renderApp("/", api);

    await user.type(await box(), "Back up my files");
    await user.click(screen.getByRole("button", { name: "Ask" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("The router agent is restarting");
  });

  it("has no accessibility violations on the home screen", async () => {
    const { container } = renderApp("/");
    await screen.findByRole("list", { name: "Residents, most recent first" });

    await expectNoAxeViolations(container);
  });
});
