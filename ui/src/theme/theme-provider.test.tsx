// SPDX-License-Identifier: MIT
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { media } from "../test/setup";
import { FakeApi, fixtures, renderWith } from "../test/fake-api";
import SettingsPanel from "../panels/settings";
import { defaultTheme } from "./theme";

describe("ThemeProvider and the settings panel", () => {
  it("applies the bundled theme in light mode by default", async () => {
    renderWith(<SettingsPanel />);

    await screen.findByRole("radio", { name: "System" });
    expect(document.documentElement.dataset.mode).toBe("light");
    expect(document.documentElement.style.getPropertyValue("--color-background")).toBe(
      defaultTheme.modes.light.color.background,
    );
  });

  it("follows a dark system setting", async () => {
    media.dark = true;
    renderWith(<SettingsPanel />);

    await screen.findByRole("radio", { name: "System" });
    expect(document.documentElement.dataset.mode).toBe("dark");
  });

  it("switches mode and saves the choice for the human", async () => {
    const user = userEvent.setup();
    const { api } = renderWith(<SettingsPanel />);

    await user.click(await screen.findByRole("radio", { name: "Dark" }));

    expect(screen.getByRole("radio", { name: "Dark" })).toBeChecked();
    expect(document.documentElement.dataset.mode).toBe("dark");
    await waitFor(() => {
      expect(api.calls).toContain("setThemeSelection dark");
    });
    expect(window.localStorage.getItem("agentsone.mode")).toBe("dark");
  });

  it("applies an imported theme the human selected", async () => {
    const api = new FakeApi();
    const imported = {
      ...fixtures.theme,
      builtin: false,
      theme: {
        ...fixtures.theme.theme,
        modes: {
          ...fixtures.theme.theme.modes,
          light: {
            ...fixtures.theme.theme.modes.light,
            color: { ...fixtures.theme.theme.modes.light.color, background: "#FAFAFA" },
          },
        },
      },
    };
    api.getTheme = () => Promise.resolve(imported);
    renderWith(<SettingsPanel />, api);

    await waitFor(() => {
      expect(document.documentElement.style.getPropertyValue("--color-background")).toBe("#FAFAFA");
    });
  });
});
