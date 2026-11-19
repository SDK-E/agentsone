// SPDX-License-Identifier: MIT
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoAxeViolations } from "../test/axe";
import { FakeApi, renderApp, unauthorized } from "../test/fake-api";

function signedOut() {
  const api = new FakeApi();
  api.me = unauthorized();
  return api;
}

describe("Sign in and sign out", () => {
  it("sends a visitor without a session to sign in", async () => {
    renderApp("/", signedOut());

    expect(await screen.findByRole("heading", { name: "Sign in" })).toBeVisible();
  });

  it("signs in and opens home", async () => {
    const user = userEvent.setup();
    const { api } = renderApp("/sign-in", signedOut());

    await user.type(screen.getByRole("textbox", { name: "Name" }), "ada");
    await user.type(screen.getByLabelText("Password"), "secret{Enter}");

    expect(await screen.findByRole("heading", { name: "Ask the society" })).toBeVisible();
    expect(api.calls).toContain("createSession ada");
    expect(screen.getByText("Ada Lovelace")).toBeVisible();
  });

  it("says when the name and password do not match", async () => {
    const user = userEvent.setup();
    renderApp("/sign-in", signedOut());

    await user.type(screen.getByRole("textbox", { name: "Name" }), "wrong");
    await user.type(screen.getByLabelText("Password"), "secret");
    await user.click(screen.getByRole("button", { name: "Sign in" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Name or password is wrong.");
  });

  it("signs out from the top bar", async () => {
    const user = userEvent.setup();
    const { api } = renderApp("/");

    await user.click(await screen.findByRole("button", { name: "Sign out" }));

    expect(await screen.findByRole("heading", { name: "Sign in" })).toBeVisible();
    expect(api.calls).toContain("deleteSession");
  });

  it("offers a skip link to the main content", async () => {
    renderApp("/");

    expect(await screen.findByRole("link", { name: "Skip to main content" })).toHaveAttribute(
      "href",
      "#main",
    );
  });

  it("has no accessibility violations", async () => {
    const { container } = renderApp("/sign-in", signedOut());

    await expectNoAxeViolations(container);
  });
});
