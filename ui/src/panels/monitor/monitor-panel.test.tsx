// SPDX-License-Identifier: MIT
import { act, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { expectNoAxeViolations } from "../../test/axe";
import { FakeApi, fixtures, problem, renderWith } from "../../test/fake-api";
import MonitorPanel from "./index";

const agentList = () => screen.findByRole("list", { name: "Agents, most recent first" });

describe("MonitorPanel", () => {
  it("shows the total budget as a hard limit held outside the machine", async () => {
    renderWith(<MonitorPanel />);

    const budget = await screen.findByRole("region", { name: "Budget" });
    expect(await within(budget).findByText("$37.16 left of $50.00")).toBeVisible();
    expect(within(budget).getByRole("meter", { name: "Total budget used" })).toHaveAttribute(
      "aria-valuenow",
      "12840000",
    );
    expect(within(budget).getByText("Hard limit")).toBeVisible();
    expect(
      within(budget).getByText(
        "The total is a hard limit. The edge holds it outside the machine and people set it on the control page. No agent can raise it.",
      ),
    ).toBeVisible();
  });

  it("marks the defence reserve as a soft limit and says why", async () => {
    renderWith(<MonitorPanel />);

    const budget = await screen.findByRole("region", { name: "Budget" });
    const reserve = await within(budget).findByText("Defence reserve: $0.41 of $5.00 used");
    expect(within(reserve).getByText("Soft limit")).toBeVisible();
    expect(
      within(budget).getByText(
        "A root agent inside the machine can pose as another agent at the gateway, so this reserve can be got around.",
      ),
    ).toBeVisible();
  });

  it("says nothing about a soft limit when the defence reserve is off", async () => {
    const api = new FakeApi();
    api.budget = { ...fixtures.budget, defence: { ...fixtures.budget.defence, enabled: false } };
    renderWith(<MonitorPanel />, api);

    const budget = await screen.findByRole("region", { name: "Budget" });
    expect(await within(budget).findByText("The defence reserve is switched off.")).toBeVisible();
    expect(within(budget).queryByText("Soft limit")).not.toBeInTheDocument();
  });

  it("tells the person plainly when the budget is used up", async () => {
    const api = new FakeApi();
    api.budget = { ...fixtures.budget, state: "exhausted", remaining_micros: 0 };
    renderWith(<MonitorPanel />, api);

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "The budget is used up. Agents pause until you add more from the control page.",
    );
  });

  it("shows machine usage in words and meters", async () => {
    renderWith(<MonitorPanel />);

    const machine = await screen.findByRole("region", { name: "The machine" });
    expect(await within(machine).findByText("11.5% of 4 cores")).toBeVisible();
    expect(within(machine).getByText("3.0 GiB of 8.0 GiB")).toBeVisible();
    expect(within(machine).getByText(/Up 4 h 45 min/)).toBeVisible();
    expect(within(machine).getByRole("meter", { name: "Disk /var" })).toBeVisible();
    expect(within(machine).getByText("13.8 GiB of 16.0 GiB")).toBeVisible();
  });

  it("lists each agent with its goal, state and soft budget share", async () => {
    renderWith(<MonitorPanel />);

    const items = within(await agentList()).getAllByRole("listitem");
    expect(items).toHaveLength(fixtures.live.length);
    const scribe = items.find((li) => within(li).queryByText("Scribe"));
    if (!scribe) throw new Error("no scribe row");
    expect(within(scribe).getByText("Asleep")).toBeVisible();
    expect(within(scribe).getByText(/Goal: clear docs for humans/)).toBeVisible();
    expect(within(scribe).getByText("$1.77 of $2.00 used")).toBeVisible();
    expect(within(scribe).getByText("Soft limit")).toBeVisible();
    expect(within(scribe).getByText("Running low")).toBeVisible();
  });

  it("says when an agent shares its manager's remainder", async () => {
    renderWith(<MonitorPanel />);

    const items = within(await agentList()).getAllByRole("listitem");
    const waker = items.find((li) => within(li).queryByText("Waker"));
    if (!waker) throw new Error("no waker row");
    expect(within(waker).getByText("$0.41 used, no share set")).toBeVisible();
  });

  it("moves an agent to the top when the live stream reports new work", async () => {
    const api = new FakeApi();
    renderWith(<MonitorPanel />, api);
    await agentList();
    const keeper = fixtures.live.find((e) => e.agent === "keeper");
    if (!keeper) throw new Error("no keeper");

    act(() => {
      api.liveStream.emit({
        kind: "entry",
        entry: {
          ...keeper,
          state: "working",
          one_line: { text: "Now: trimming old logs.", updated_at: "2026-11-19T18:41:00Z" },
        },
      });
    });

    const first = within(await agentList()).getAllByRole("listitem")[0];
    if (!first) throw new Error("no row");
    expect(within(first).getByText("Now: trimming old logs.")).toBeVisible();
    expect(screen.getByText("Live")).toBeVisible();
  });

  it("falls back to polling and says so when the stream closes", async () => {
    const api = new FakeApi();
    renderWith(<MonitorPanel />, api);
    await agentList();

    act(() => {
      api.liveStream.close();
    });

    expect(screen.getByText("Updates every 10 seconds")).toBeVisible();
  });

  it("shows an empty society and no budget rows", async () => {
    const api = new FakeApi();
    api.live = [];
    api.split = { ...fixtures.split, nodes: [] };
    renderWith(<MonitorPanel />, api);

    expect(await screen.findByRole("heading", { name: "No agents yet" })).toBeVisible();
  });

  it("keeps working when one section fails and offers a retry for it", async () => {
    const api = new FakeApi();
    api.metrics = problem(503, "Metrics unavailable", "The machine could not be read.");
    renderWith(<MonitorPanel />, api);

    const machine = await screen.findByRole("region", { name: "The machine" });
    expect(await within(machine).findByRole("alert")).toHaveTextContent(
      "The machine could not be read.",
    );
    expect(within(machine).getByRole("button", { name: "Try again" })).toBeEnabled();
    expect(await agentList()).toBeVisible();
  });

  it("shows a budget error without hiding the agents", async () => {
    const api = new FakeApi();
    api.budget = problem(503, "Budget unavailable", "The control page has not reported yet.");
    renderWith(<MonitorPanel />, api);

    const budget = await screen.findByRole("region", { name: "Budget" });
    expect(await within(budget).findByRole("alert")).toHaveTextContent(
      "The control page has not reported yet.",
    );
    expect(await agentList()).toBeVisible();
  });

  it("keeps a very long agent name and goal inside the row", async () => {
    const api = new FakeApi();
    const long = {
      ...fixtures.live[0],
      agent: "a-very-long-agent-name-thirty1",
      display_name: "A very long display name that keeps going and going for a good while",
      one_line: {
        text: "Goal: ".concat("keep going ".repeat(40)),
        updated_at: "2026-11-19T18:41:00Z",
      },
    } as (typeof fixtures.live)[number];
    api.live = [long];
    renderWith(<MonitorPanel />, api);

    expect(await screen.findByText(/A very long display name/)).toBeVisible();
  });

  it("has no accessibility violations", async () => {
    const { container } = renderWith(<MonitorPanel />);
    await agentList();
    await screen.findByText("3.0 GiB of 8.0 GiB");

    await expectNoAxeViolations(container);
  });
});
