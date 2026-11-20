// SPDX-License-Identifier: MIT
import { fireEvent, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, describe, expect, it, vi } from "vitest";

import { PANEL_IDS } from "../panels/registry";
import { expectNoAxeViolations } from "../test/axe";
import { renderApp, renderWith } from "../test/fake-api";
import { media } from "../test/setup";
import { PanelsArea } from "./panel-tabs";

describe("Panels", () => {
  // Panels load lazily. The first load of a panel's module is its first transform, which
  // takes about 300 ms on an idle machine and over a second on a busy one, longer than
  // findBy waits. Load the Tasks panel up front, so the tests below wait on the panel's
  // own state (its list), never on a cold module.
  beforeAll(async () => {
    await import("../panels/tasks");
  });

  it("lists every panel from the registry as a tab, Monitor first", () => {
    renderWith(<PanelsArea />);

    const tabs = screen.getAllByRole("tab");
    expect(tabs.map((t) => t.textContent)).toEqual([
      "Monitor",
      "Tasks",
      "Memory",
      "Plugins",
      "IDE",
      "Desktop",
      "Machine",
      "Audit",
      "Settings",
    ]);
    expect(tabs).toHaveLength(PANEL_IDS.length);
    expect(screen.getByRole("tabpanel", { name: "Monitor" })).toBeVisible();
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
    expect(await within(panel).findByRole("list", { name: "Tasks, newest first" })).toBeVisible();
  });

  it("opens every panel in its own tab panel", async () => {
    const user = userEvent.setup();
    renderWith(<PanelsArea />);

    for (const name of ["IDE", "Desktop", "Machine", "Audit"]) {
      await user.click(screen.getByRole("tab", { name }));
      expect(screen.getByRole("tabpanel", { name })).toBeVisible();
    }
  });

  it("brings the chosen tab into view when the tabs do not all fit", async () => {
    const user = userEvent.setup();
    const scrolled = vi.spyOn(Element.prototype, "scrollIntoView");
    renderWith(<PanelsArea />);

    await user.click(screen.getByRole("tab", { name: "Settings" }));

    const lastCall = scrolled.mock.contexts.at(-1) as HTMLElement;
    expect(lastCall).toBe(screen.getByRole("tab", { name: "Settings" }));
    expect(scrolled).toHaveBeenLastCalledWith({ inline: "nearest", block: "nearest" });
    scrolled.mockRestore();
  });

  it("shows a cue where more tabs are hidden, and drops it at the end", () => {
    renderWith(<PanelsArea />);
    const list = screen.getByRole("tablist");
    const scroller = list.parentElement;
    if (!scroller) throw new Error("the tab row has no wrapper");
    const first = screen.getByRole("tab", { name: "Monitor" });
    const last = screen.getByRole("tab", { name: "Settings" });
    const box = (left: number, right: number) =>
      ({ left, right, width: right - left, top: 0, bottom: 44, height: 44 }) as DOMRect;
    // The row is 300 wide. The tabs are 700 wide in all, scrolled by `scrolled` pixels, with 4 pixels
    // of padding on each side of the row: a side has more only when its end tab is not wholly in view.
    const scrollTo = (scrolled: number) => {
      vi.spyOn(list, "getBoundingClientRect").mockReturnValue(box(0, 300));
      vi.spyOn(first, "getBoundingClientRect").mockReturnValue(box(4 - scrolled, 80 - scrolled));
      vi.spyOn(last, "getBoundingClientRect").mockReturnValue(box(624 - scrolled, 704 - scrolled));
      fireEvent.scroll(list);
    };

    scrollTo(0);
    expect(scroller).toHaveAttribute("data-more-end", "true");
    expect(scroller).not.toHaveAttribute("data-more-start");

    scrollTo(200);
    expect(scroller).toHaveAttribute("data-more-start", "true");
    expect(scroller).toHaveAttribute("data-more-end", "true");

    // Scrolled until the last tab sits at the edge: the row's own end padding is not "more".
    scrollTo(404);
    expect(scroller).toHaveAttribute("data-more-start", "true");
    expect(scroller).not.toHaveAttribute("data-more-end");

    // Scrolled back by a little: the first tab is still whole, so there is nothing more to the start.
    scrollTo(3);
    expect(scroller).not.toHaveAttribute("data-more-start");
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
    await screen.findByText("Hard limit");

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
