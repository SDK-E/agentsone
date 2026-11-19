// SPDX-License-Identifier: MIT
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nextProvider } from "react-i18next";
import { describe, expect, it, vi } from "vitest";

import { i18n } from "../i18n/i18n";
import { expectNoAxeViolations } from "../test/axe";
import { ModeSwitch } from "./mode-switch";

const renderSwitch = (onChange = vi.fn()) =>
  render(
    <I18nextProvider i18n={i18n}>
      <ModeSwitch mode="system" onChange={onChange} />
    </I18nextProvider>,
  );

describe("ModeSwitch", () => {
  it("offers system, light and dark with the current one chosen", () => {
    renderSwitch();

    expect(screen.getByRole("group", { name: "Appearance" })).toBeVisible();
    expect(screen.getByRole("radio", { name: "System" })).toBeChecked();
    expect(screen.getByRole("radio", { name: "Light" })).not.toBeChecked();
  });

  it("reports the mode the person picks", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    renderSwitch(onChange);

    await user.click(screen.getByRole("radio", { name: "Dark" }));

    expect(onChange).toHaveBeenCalledWith("dark");
  });

  it("has no accessibility violations", async () => {
    const { container } = renderSwitch();

    await expectNoAxeViolations(container);
  });
});
