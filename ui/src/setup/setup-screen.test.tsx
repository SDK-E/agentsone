// SPDX-License-Identifier: MIT
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoAxeViolations } from "../test/axe";
import { FakeApi, fixtures, renderApp } from "../test/fake-api";

/** A fresh machine: no human yet, and the Claude engine off. */
function firstBoot() {
  const api = new FakeApi();
  api.setup = {
    ...fixtures.setup,
    phase: "needs-human",
    first_human_created: false,
    licence_accepted: false,
    engine: { ...fixtures.engineFetch, state: "waiting-licence", progress: 0, bytes_done: 0 },
  };
  return api;
}

describe("SetupScreen", () => {
  it("sends a fresh machine to setup from home", async () => {
    renderApp("/", firstBoot());

    expect(await screen.findByRole("heading", { name: "Create your account" })).toBeVisible();
  });

  it("explains every rule the form breaks", async () => {
    const user = userEvent.setup();
    renderApp("/setup", firstBoot());

    await user.click(await screen.findByRole("button", { name: "Create account" }));

    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("The name must start with a letter");
    expect(alert).toHaveTextContent("Add a display name");
    expect(alert).toHaveTextContent("at least 12 characters");
  });

  it("is done once the first human exists, with no Claude engine step", async () => {
    const user = userEvent.setup();
    const { api } = renderApp("/setup", firstBoot());

    await user.type(await screen.findByRole("textbox", { name: "Name" }), "Ada");
    await user.type(screen.getByRole("textbox", { name: "Display name" }), "Ada Lovelace");
    await user.type(screen.getByLabelText("Password"), "correct horse battery");
    await user.type(screen.getByLabelText("Password again"), "correct horse battery");
    await user.click(screen.getByRole("button", { name: "Create account" }));

    expect(await screen.findByRole("heading", { name: "Setup is done" })).toBeVisible();
    expect(api.calls).toEqual(["createFirstHuman ada", "createSession ada"]);
    expect(screen.getByText(/The Claude engine is off/)).toBeVisible();
    expect(screen.queryByRole("button", { name: "Accept and download" })).toBeNull();
    expect(screen.queryByRole("progressbar")).toBeNull();
    expect(screen.getByRole("link", { name: "Go to the society" })).toHaveAttribute("href", "/");
  });

  it("says setup is done on a ready machine", async () => {
    renderApp("/setup");

    expect(await screen.findByRole("heading", { name: "Setup is done" })).toBeVisible();
    // This machine's engine was turned on, so there is no hint to turn it on.
    expect(screen.queryByText(/The Claude engine is off/)).toBeNull();
  });

  it("never asks for the licence, even from an older daemon", async () => {
    const api = new FakeApi();
    api.setup = { ...fixtures.setup, phase: "needs-licence", licence_accepted: false };
    renderApp("/setup", api);

    expect(await screen.findByRole("heading", { name: "Setup is done" })).toBeVisible();
    expect(screen.queryByRole("button", { name: "Accept and download" })).toBeNull();
  });

  it("has no accessibility violations", async () => {
    const { container } = renderApp("/setup", firstBoot());
    await screen.findByRole("heading", { name: "Create your account" });

    await expectNoAxeViolations(container);
  });
});
