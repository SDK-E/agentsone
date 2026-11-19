// SPDX-License-Identifier: MIT
import { act, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { expectNoAxeViolations } from "../test/axe";
import { FakeApi, fixtures, renderWith } from "../test/fake-api";
import MonitorPanel from "../panels/monitor/index";
import { LiveSociety } from "./live-society";

const REASON = "The model gateway is not reachable.";

/** The seeded society with Builder stopped with an error, and Builder's own record to match. */
function failedBuilder(reason: string | null = REASON) {
  const api = new FakeApi();
  api.live = fixtures.live.map((e) => (e.agent === "builder" ? { ...e, state: "failed" } : e));
  api.agents = { builder: { ...fixtures.failedAgent, state_reason: reason } };
  return api;
}

const builderCard = async () => {
  const list = await screen.findByRole("list", { name: "Residents, most recent first" });
  const card = within(list)
    .getAllByRole("listitem")
    .find((li) => within(li).queryByRole("link", { name: "Open chat with Builder" }));
  if (!card) throw new Error("no Builder card");
  return card;
};

describe("why an agent stopped, in the society list", () => {
  it("says why under a stopped agent and nowhere else", async () => {
    renderWith(<LiveSociety />, failedBuilder());

    const card = await builderCard();
    expect(await within(card).findByText(REASON)).toBeVisible();
    expect(within(card).getByText("Why it stopped:")).toBeVisible();
    expect(within(card).getByText("Stopped with an error")).toBeVisible();
    expect(screen.getAllByText(REASON)).toHaveLength(1);
  });

  it("shows nothing extra when the agent gave no reason", async () => {
    const api = failedBuilder(null);
    renderWith(<LiveSociety />, api);

    const card = await builderCard();
    await screen.findByText("Stopped with an error");
    expect(api.calls).toContain("getAgent builder");
    expect(within(card).queryByText("Why it stopped:")).not.toBeInTheDocument();
  });

  it("does not ask for the reason of an agent that is not stopped", async () => {
    const api = new FakeApi();
    renderWith(<LiveSociety />, api);

    await builderCard();
    expect(api.calls.filter((c) => c.startsWith("getAgent"))).toEqual([]);
    expect(screen.queryByText("Why it stopped:")).not.toBeInTheDocument();
  });

  it("keeps a long reason whole so it can wrap", async () => {
    const api = failedBuilder(fixtures.failedAgentLong.state_reason);
    renderWith(<LiveSociety />, api);

    const card = await builderCard();
    expect(
      await within(card).findByText(/ask a human to look at the edge service log\.$/),
    ).toBeVisible();
    expect(card).toHaveTextContent(fixtures.failedAgentLong.state_reason ?? "never");
  });

  it("updates the card when a state frame brings a new reason", async () => {
    const api = failedBuilder();
    renderWith(<LiveSociety />, api);
    const card = await builderCard();
    await within(card).findByText(REASON);

    act(() => {
      api.outputStream.emit({
        kind: "state",
        agent: "builder",
        state: "failed",
        state_reason: "The runner stopped with an error and did not start again.",
        time: "2026-11-19T18:42:00Z",
      });
    });

    expect(
      await within(card).findByText("The runner stopped with an error and did not start again."),
    ).toBeVisible();
    expect(within(card).queryByText(REASON)).not.toBeInTheDocument();
  });

  it("drops the reason when a state frame says the agent runs again", async () => {
    const api = failedBuilder();
    renderWith(<LiveSociety />, api);
    const card = await builderCard();
    await within(card).findByText(REASON);

    act(() => {
      api.outputStream.emit({
        kind: "state",
        agent: "builder",
        state: "working",
        time: "2026-11-19T18:43:00Z",
      });
    });

    await waitFor(() => {
      expect(within(card).queryByText(REASON)).not.toBeInTheDocument();
    });
  });

  it("has no accessibility violations", async () => {
    const { container } = renderWith(<LiveSociety />, failedBuilder());
    await screen.findByText(REASON);

    await expectNoAxeViolations(container);
  });
});

describe("why an agent stopped, in the Monitor", () => {
  it("says why under the stopped agent's row", async () => {
    renderWith(<MonitorPanel />, failedBuilder());

    const list = await screen.findByRole("list", { name: "Agents, most recent first" });
    const row = within(list)
      .getAllByRole("listitem")
      .find((li) => within(li).queryByRole("link", { name: "Open chat with Builder" }));
    if (!row) throw new Error("no Builder row");
    expect(await within(row).findByText(REASON)).toBeVisible();
    expect(within(row).getByText("Why it stopped:")).toBeVisible();
  });

  it("has no accessibility violations", async () => {
    const { container } = renderWith(<MonitorPanel />, failedBuilder());
    await screen.findByText(REASON);

    await expectNoAxeViolations(container);
  });
});
