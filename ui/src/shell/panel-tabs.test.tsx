// SPDX-License-Identifier: MIT
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { PANEL_IDS } from "../panels/registry";
import { expectNoAxeViolations } from "../test/axe";
import { renderApp, renderWith } from "../test/fake-api";
import { media } from "../test/setup";
import { PanelsArea } from "./panel-tabs";

describe("Panels", () => {
  it("lists every panel from the registry as a tab, Monitor first", async () => {
    renderWith(<PanelsArea />);

    const tabs = screen.getAllByRole("tab");
    expect(tabs.map((t) => t.textContent)).toEqual([
      "Monitor",
      "Tasks",
      "Memory",
      "Plugins",
      "IDE",
      "Machine",
      "Audit",
      "Settings",
    ]);
    expect(tabs).toHaveLength(PANEL_IDS.length);
    expect(await screen.findByRole("heading", { name: "Monitor" })).toBeVisible();
  });

  it("moves between tabs with the arrow, Home and End keys", async () => {
    const user = userEvent.setup();
    renderWith(<PanelsArea />);

    await user.click(screen.getByRole("tab", { name: "Monitor" }));
    await user.keyboard("{ArrowLeft}");
    expect(screen.getByRole("tab", { name: "Settings" })).toHaveFocus();
    await user.keyboard("{ArrowRight}");
    expect(screen.getByRole("tab", { name: "Monitor" })).toHaveFocus();
    await user.keyboard("{End}");
    await user.keyboard("{Home}");
    await user.keyboard("{ArrowRight}");

    const tasks = screen.getByRole("tab", { name: "Tasks" });
    expect(tasks).toHaveFocus();
    expect(tasks).toHaveAttribute("aria-selected", "true");
    const panel = screen.getByRole("tabpanel", { name: "Tasks" });
    expect(await within(panel).findByText(/Tasks the agents gave each other/)).toBeVisible();
  });

  it("opens every placeholder panel without error", async () => {
    const user = userEvent.setup();
    renderWith(<PanelsArea />);

    for (const name of ["Memory", "Plugins", "IDE", "Machine", "Audit"]) {
      await user.click(screen.getByRole("tab", { name }));
      expect(await screen.findByRole("heading", { name })).toBeVisible();
    }
  });

  it("puts the panels in a sheet on a phone", async () => {
    const user = userEvent.setup();
    media.phone = true;
    renderWith(<PanelsArea />);

    expect(screen.queryByRole("tab")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Open panels" }));
    const sheet = screen.getByRole("dialog", { name: "Panels" });
    expect(within(sheet).getByRole("tab", { name: "Monitor" })).toBeVisible();

    await user.click(within(sheet).getByRole("button", { name: "Close" }));
    expect(screen.queryByRole("tab")).not.toBeInTheDocument();
  });

  it("has no accessibility violations", async () => {
    const { container } = renderWith(<PanelsArea />);
    await screen.findByRole("heading", { name: "Monitor" });

    await expectNoAxeViolations(container);
  });
});

describe("Not found", () => {
  it("sends a lost visitor home", () => {
    renderApp("/no/such/page");

    expect(screen.getByRole("heading", { name: "Nothing lives here" })).toBeVisible();
    expect(screen.getByRole("link", { name: "Go to the society" })).toHaveAttribute("href", "/");
  });
});
