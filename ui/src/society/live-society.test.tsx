// SPDX-License-Identifier: MIT
import { act, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoAxeViolations } from "../test/axe";
import { FakeApi, fixtures, problem, renderWith } from "../test/fake-api";
import { LiveSociety } from "./live-society";
import { relativeTime } from "./relative-time";
import { applyLiveFrame } from "./use-live-list";

const residents = () => screen.findByRole("list", { name: "Residents, most recent first" });

describe("LiveSociety", () => {
  it("shows each resident with its one-line, state and kind", async () => {
    renderWith(<LiveSociety />);

    const list = await residents();
    const items = within(list).getAllByRole("listitem");
    expect(items).toHaveLength(fixtures.live.length);
    const builder = items[3];
    if (!builder) throw new Error("no fourth resident");
    expect(within(builder).getByRole("link", { name: "Open chat with Builder" })).toHaveAttribute(
      "href",
      "/agents/builder/chat",
    );
    expect(within(builder).getByText(/Goal: ship the docs site/)).toBeVisible();
    expect(within(builder).getByText("Working")).toBeVisible();
    expect(within(builder).getByText("Always on")).toBeVisible();
    expect(screen.getByText("6 residents, 4 awake")).toBeVisible();
    expect(screen.getByText("Live")).toBeVisible();
  });

  it("moves an agent to the top when its one-line changes on the stream", async () => {
    const api = new FakeApi();
    renderWith(<LiveSociety />, api);
    await residents();
    const scribe = fixtures.live.find((e) => e.agent === "scribe");
    if (!scribe) throw new Error("fixture has no scribe");

    act(() => {
      api.liveStream.emit({
        kind: "entry",
        entry: {
          ...scribe,
          state: "working",
          one_line: { text: "Now: writing the budget page.", updated_at: "2026-11-19T18:40:00Z" },
        },
      });
    });

    const first = within(await residents()).getAllByRole("listitem")[0];
    if (!first) throw new Error("no resident");
    expect(within(first).getByText("Now: writing the budget page.")).toBeVisible();
  });

  it("removes a retired agent and says when it falls back to polling", async () => {
    const api = new FakeApi();
    renderWith(<LiveSociety />, api);
    await residents();

    act(() => {
      api.liveStream.emit({ kind: "removed", agent: "keeper" });
      api.liveStream.emit({ kind: "heartbeat", time: "2026-11-19T18:40:00Z" });
      api.liveStream.close();
    });

    expect(screen.queryByRole("link", { name: "Open chat with Keeper" })).not.toBeInTheDocument();
    expect(screen.getByText("Updates every 10 seconds")).toBeVisible();
  });

  it("shows an empty society honestly", async () => {
    const api = new FakeApi();
    api.live = [];
    renderWith(<LiveSociety />, api);

    expect(await screen.findByRole("heading", { name: "Nobody lives here yet" })).toBeVisible();
  });

  it("says what went wrong and offers a retry", async () => {
    const api = new FakeApi();
    api.live = problem(500, "Broken", "The bus is down.");
    renderWith(<LiveSociety />, api);

    expect(await screen.findByRole("alert")).toHaveTextContent("The bus is down.");
    expect(screen.getByRole("button", { name: "Try again" })).toBeEnabled();
  });

  it("switches to the org chart", async () => {
    const user = userEvent.setup();
    renderWith(<LiveSociety />);
    await residents();

    await user.click(screen.getByRole("button", { name: "Org chart" }));

    const chart = await screen.findByRole("list", { name: "Org chart, who hired whom" });
    expect(within(chart).getByRole("link", { name: "Founder" })).toBeVisible();
    expect(within(chart).getByText("Root")).toBeVisible();
    expect(screen.getByRole("button", { name: "Org chart" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
  });

  it("shows an org chart error", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.chart = problem(503, "Chart unavailable");
    renderWith(<LiveSociety />, api);
    await residents();

    await user.click(screen.getByRole("button", { name: "Org chart" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Chart unavailable");
  });

  it("has no accessibility violations", async () => {
    const { container } = renderWith(<LiveSociety />);
    await residents();

    await expectNoAxeViolations(container);
  });
});

describe("applyLiveFrame", () => {
  it("adds a new agent at the top", () => {
    const [first] = fixtures.live;
    if (!first) throw new Error("empty fixture");
    const fresh = { ...first, agent: "newcomer" };

    expect(applyLiveFrame([first], { kind: "entry", entry: fresh }).map((e) => e.agent)).toEqual([
      "newcomer",
      first.agent,
    ]);
  });
});

describe("relativeTime", () => {
  const now = Date.parse("2026-11-19T18:40:00Z");

  it.each([
    ["2026-11-19T18:39:50Z", "now"],
    ["2026-11-19T18:38:00Z", "2 minutes ago"],
    ["2026-11-19T16:40:00Z", "2 hours ago"],
    ["2026-11-18T18:40:00Z", "yesterday"],
  ])("%s is %s", (iso, expected) => {
    expect(relativeTime(iso, now)).toBe(expected);
  });
});
