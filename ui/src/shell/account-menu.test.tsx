// SPDX-License-Identifier: MIT
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nextProvider } from "react-i18next";
import { describe, expect, it, vi } from "vitest";

import { i18n } from "../i18n/i18n";
import { expectNoAxeViolations } from "../test/axe";
import { AccountMenu } from "./account-menu";

function renderMenu(onSignOut = vi.fn()) {
  return render(
    <I18nextProvider i18n={i18n}>
      <AccountMenu name="Ada Lovelace" signingOut={false} onSignOut={onSignOut} />
      <p>Elsewhere</p>
    </I18nextProvider>,
  );
}

const toggle = () => screen.getByRole("button", { name: "Account, Ada Lovelace" });

describe("AccountMenu", () => {
  it("opens to show who is visiting and sign out", async () => {
    const user = userEvent.setup();
    const onSignOut = vi.fn();
    renderMenu(onSignOut);

    expect(toggle()).toHaveAttribute("aria-expanded", "false");
    await user.click(toggle());

    expect(toggle()).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByText("Visiting as Ada Lovelace")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Sign out" }));
    expect(onSignOut).toHaveBeenCalledOnce();
  });

  it("closes on Escape and gives focus back", async () => {
    const user = userEvent.setup();
    renderMenu();

    await user.click(toggle());
    await user.keyboard("{Escape}");

    expect(screen.queryByRole("button", { name: "Sign out" })).not.toBeInTheDocument();
    expect(toggle()).toHaveFocus();
  });

  it("closes on a click elsewhere", async () => {
    const user = userEvent.setup();
    renderMenu();

    await user.click(toggle());
    await user.click(screen.getByText("Elsewhere"));

    expect(screen.queryByRole("button", { name: "Sign out" })).not.toBeInTheDocument();
  });

  it("has no accessibility violations when open", async () => {
    const user = userEvent.setup();
    const { container } = renderMenu();

    await user.click(toggle());

    await expectNoAxeViolations(container);
  });
});
