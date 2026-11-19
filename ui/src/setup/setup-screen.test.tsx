// SPDX-License-Identifier: MIT
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoAxeViolations } from "../test/axe";
import { FakeApi, fixtures, renderApp } from "../test/fake-api";

function firstBoot() {
  const api = new FakeApi();
  api.setup = { ...fixtures.setup, phase: "needs-human", first_human_created: false };
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

  it("creates the first human, signs in, then shows the licence with honest download text", async () => {
    const user = userEvent.setup();
    const { api } = renderApp("/setup", firstBoot());

    await user.type(await screen.findByRole("textbox", { name: "Name" }), "Ada");
    await user.type(screen.getByRole("textbox", { name: "Display name" }), "Ada Lovelace");
    await user.type(screen.getByLabelText("Password"), "correct horse battery");
    await user.type(screen.getByLabelText("Password again"), "correct horse battery");
    await user.click(screen.getByRole("button", { name: "Create account" }));

    expect(await screen.findByRole("heading", { name: "The Claude engine" })).toBeVisible();
    expect(api.calls).toEqual(["createFirstHuman ada", "createSession ada"]);
    expect(screen.getByText(/Nothing downloads before you accept/)).toBeVisible();
    expect(screen.getByRole("article", { name: "Claude engine licence text" })).toHaveTextContent(
      "Claude Code licence",
    );
  });

  it("accepts the licence and shows the download progress", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.setup = { ...fixtures.setup, phase: "needs-licence" };
    renderApp("/setup", api);

    await user.click(await screen.findByRole("button", { name: "Accept and download" }));

    expect(await screen.findByRole("progressbar", { name: "Engine download" })).toHaveAttribute(
      "value",
      "0.42",
    );
    expect(api.calls).toContain(`acceptEngineLicence ${fixtures.licence.version}`);
    expect(screen.getByText("90 of 215 MB")).toBeVisible();
    expect(screen.getByRole("link", { name: "Go to the society" })).toHaveAttribute("href", "/");
  });

  it("says setup is done on a ready machine", async () => {
    renderApp("/setup");

    expect(await screen.findByRole("heading", { name: "Setup is done" })).toBeVisible();
  });

  it("has no accessibility violations", async () => {
    const { container } = renderApp("/setup", firstBoot());
    await screen.findByRole("heading", { name: "Create your account" });

    await expectNoAxeViolations(container);
  });
});
