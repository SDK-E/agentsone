// SPDX-License-Identifier: MIT
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import type { AuditRecord } from "../../api/api";
import { expectNoAxeViolations } from "../../test/axe";
import { FakeApi, fixtures, problem, renderWith } from "../../test/fake-api";
import AuditPanel from "./index";

const records = () => screen.findByRole("list", { name: "Records, newest first" });
const rowFor = async (text: string) => {
  const items = within(await records()).getAllByRole("listitem");
  const row = items.find((li) => within(li).queryByText(text));
  if (!row) throw new Error(`no row with ${text}`);
  return row;
};

const nth = (items: HTMLElement[], index: number): HTMLElement => {
  const item = items[index];
  if (!item) throw new Error(`no item ${String(index)}`);
  return item;
};

/** `count` records, numbered from 1, each with its own action so a test can tell them apart. */
const many = (count: number): AuditRecord[] => {
  const [first] = fixtures.audit;
  if (!first) throw new Error("no fixture");
  return Array.from({ length: count }, (_, i) => ({
    ...first,
    seq: i + 1,
    action: `thing.number-${String(i + 1)}`,
  }));
};
const upTo = (count: number) => ({ ...fixtures.verification, to_seq: count, checked: count });

describe("AuditPanel chain check", () => {
  it("says the chain is intact and how much of it the edge also holds", async () => {
    renderWith(<AuditPanel />);

    const check = await screen.findByRole("region", { name: "Chain check" });
    expect(await within(check).findByText("The chain is intact.")).toBeVisible();
    expect(within(check).getByText("5 records checked, up to record 5.")).toBeVisible();
    expect(within(check).getByText("The edge holds its own copy up to record 4.")).toBeVisible();
    expect(within(check).getByText("1 newer record has not reached the edge yet.")).toBeVisible();
  });

  it("marks an intact chain with a tick as well as a colour", async () => {
    renderWith(<AuditPanel />);

    const verdict = await screen.findByText("The chain is intact.");
    expect(verdict.querySelector("svg")).not.toBeNull();
    expect(verdict).toHaveClass("verdict--ok");
  });

  it("raises an alarm in plain words, with a next step, when the chain is broken", async () => {
    const api = new FakeApi();
    api.verification = { ...fixtures.verification, ok: false, broken_at_seq: 3 };
    renderWith(<AuditPanel />, api);

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("The chain is broken at record 3.");
    expect(alert).toHaveTextContent("Records from there on may have been changed.");
    expect(alert).toHaveTextContent("Next step: do not rely on those records.");
    expect(alert.querySelector("svg")).not.toBeNull();
  });

  it("checks again on request", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    renderWith(<AuditPanel />, api);
    await screen.findByText("The chain is intact.");

    await user.click(screen.getByRole("button", { name: "Check again" }));

    expect(api.calls.filter((c) => c === "verifyAuditChain")).toHaveLength(2);
  });

  it("says when the check itself failed", async () => {
    const api = new FakeApi();
    api.verification = problem(503, "Down", "The check could not run.");
    renderWith(<AuditPanel />, api);

    const check = await screen.findByRole("region", { name: "Chain check" });
    expect(await within(check).findByRole("alert")).toHaveTextContent("The check could not run.");
    expect(
      await screen.findByText("The newest records cannot be found until the chain check works."),
    ).toBeVisible();
  });
});

describe("AuditPanel records", () => {
  it("lists the newest record first", async () => {
    renderWith(<AuditPanel />);

    const items = within(await records()).getAllByRole("listitem");
    expect(items.map((li) => within(li).getByText(/Record \d+/).textContent)).toEqual([
      "Record 5",
      "Record 4",
      "Record 3",
      "Record 2",
      "Record 1",
    ]);
  });

  it("labels what the edge saw as proof and what came from inside as testimony", async () => {
    renderWith(<AuditPanel />);

    const proof = await rowFor("budget.push");
    expect(within(proof).getByText("Proof")).toBeVisible();
    expect(within(proof).getByText("Seen by the edge")).toBeVisible();
    const testimony = await rowFor("agent.hire");
    expect(within(testimony).getByText("Testimony")).toBeVisible();
    expect(within(testimony).getByText("Told by the machine")).toBeVisible();
  });

  it("explains the two kinds once, above the list", async () => {
    renderWith(<AuditPanel />);
    await records();

    const legend = screen.getByRole("group", { name: "Two kinds of record" });
    expect(within(legend).getByText(/Agents cannot change it/)).toBeVisible();
    expect(within(legend).getByText(/A root agent could rewrite it/)).toBeVisible();
  });

  it("shows who did what to what, and how it went", async () => {
    renderWith(<AuditPanel />);

    const denied = await rowFor("agent.edit");
    expect(within(denied).getByText("scribe")).toBeVisible();
    expect(within(denied).getByText("Refused")).toBeVisible();
    expect(within(denied).getByText("agent keeper")).toBeVisible();
    const done = await rowFor("agent.hire");
    expect(within(done).getByText("Done")).toBeVisible();
  });

  it("calls the edge the edge, without its internal name", async () => {
    renderWith(<AuditPanel />);

    const row = await rowFor("budget.push");
    expect(within(row).getByText("The edge")).toBeVisible();
    expect(within(row).queryByText("agentsone")).not.toBeInTheDocument();
    expect(within(row).queryByText("Edge")).not.toBeInTheDocument();
  });

  it("names a plugin by its name when the record knows it, and by its id when not", async () => {
    const api = new FakeApi();
    const [install] = fixtures.audit;
    if (!install) throw new Error("no fixture");
    api.audit = [
      install,
      {
        ...install,
        seq: 2,
        action: "plugin.remove",
        target: { kind: "plugin", id: "plg_01JD7ZK3M00000000000000002" },
        details: {},
      },
    ];
    renderWith(<AuditPanel />, api);

    const named = await rowFor("plugin.install");
    expect(within(named).getByText("plugin small-seed")).toBeVisible();
    expect(within(named).queryByText(/plg_01JD7ZK3M00000000000000001/)).not.toBeInTheDocument();
    const unnamed = await rowFor("plugin.remove");
    expect(within(unnamed).getByText("plugin plg_01JD7ZK3M00000000000000002")).toBeVisible();
  });

  it("shows only proof, or only testimony, on request", async () => {
    const user = userEvent.setup();
    renderWith(<AuditPanel />);
    await records();

    await user.click(screen.getByRole("button", { name: "Proof" }));
    expect(screen.getByText("budget.push")).toBeVisible();
    expect(screen.queryByText("agent.hire")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Testimony" }));
    expect(screen.queryByText("budget.push")).not.toBeInTheDocument();
    expect(screen.getByText("agent.hire")).toBeVisible();

    await user.click(screen.getByRole("button", { name: "All" }));
    expect(screen.getByText("budget.push")).toBeVisible();
  });

  it("says when the chosen kind has nothing yet", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.audit = fixtures.audit.filter((r) => r.origin === "inside");
    renderWith(<AuditPanel />, api);
    await records();

    await user.click(screen.getByRole("button", { name: "Proof" }));

    expect(await screen.findByRole("heading", { name: "No proof records here" })).toBeVisible();
  });

  it("opens the details and the hashes of one record", async () => {
    const user = userEvent.setup();
    renderWith(<AuditPanel />);
    const row = await rowFor("agent.edit");

    await user.click(within(row).getByText("Details"));

    expect(within(row).getByText("not-below-you")).toBeVisible();
    expect(within(row).getByText("reason")).toBeVisible();
    expect(within(row).getByText(/Record hash/)).toBeVisible();
  });

  it("invites the first record when the log is empty", async () => {
    const api = new FakeApi();
    api.audit = [];
    api.verification = upTo(0);
    renderWith(<AuditPanel />, api);

    expect(await screen.findByRole("heading", { name: "No records yet" })).toBeVisible();
  });

  it("says what went wrong loading records", async () => {
    const api = new FakeApi();
    api.audit = problem(500, "Broken", "The log could not be read.");
    renderWith(<AuditPanel />, api);

    const alerts = await screen.findAllByRole("alert");
    expect(alerts.some((a) => a.textContent.includes("The log could not be read."))).toBe(true);
  });

  it("asks for the newest records first, then reaches back on request", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.audit = many(60);
    api.verification = upTo(60);
    renderWith(<AuditPanel />, api);

    const list = await records();
    const items = within(list).getAllByRole("listitem");
    expect(items).toHaveLength(25);
    expect(within(nth(items, 0)).getByText("thing.number-60")).toBeVisible();
    expect(within(list).queryByText("thing.number-35")).not.toBeInTheDocument();
    expect(api.calls).toContain("listAuditRecords after=35 limit=25");

    await user.click(screen.getByRole("button", { name: "Show older records" }));

    expect(await screen.findByText("thing.number-35")).toBeVisible();
    expect(screen.getByText("thing.number-11")).toBeVisible();
    expect(api.calls).toContain("listAuditRecords after=10 limit=25");
    const all = within(await records()).getAllByRole("listitem");
    expect(within(nth(all, 25)).getByText("thing.number-35")).toBeVisible();
  });

  it("shows every record once when the last window overlaps the one before", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.audit = many(28);
    api.verification = upTo(28);
    renderWith(<AuditPanel />, api);

    const first = within(await records()).getAllByRole("listitem");
    expect(within(nth(first, 0)).getByText("thing.number-28")).toBeVisible();

    await user.click(screen.getByRole("button", { name: "Show older records" }));
    expect(await screen.findByText("thing.number-1")).toBeVisible();

    const all = within(await records()).getAllByRole("listitem");
    expect(all).toHaveLength(28);
    expect(screen.getAllByText("thing.number-20")).toHaveLength(1);
  });

  it("stops offering older records once the first one is shown", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.audit = many(30);
    api.verification = upTo(30);
    renderWith(<AuditPanel />, api);
    await records();

    await user.click(screen.getByRole("button", { name: "Show older records" }));

    expect(await screen.findByText("thing.number-1")).toBeVisible();
    expect(screen.queryByRole("button", { name: "Show older records" })).not.toBeInTheDocument();
  });

  it("keeps long actions, targets and details inside the row", async () => {
    const api = new FakeApi();
    const first = fixtures.audit[0];
    if (!first) throw new Error("no fixture");
    api.audit = [
      {
        ...first,
        seq: 1,
        action: "machine.file.write.with.a.long.dotted.action.name",
        target: { kind: "file", id: `/home/agents/${"deeply/nested/".repeat(30)}Hero.tsx` },
        details: { error: "Permission denied while writing. ".repeat(20) } as never,
      },
    ];
    api.verification = upTo(1);
    renderWith(<AuditPanel />, api);

    expect(
      await screen.findByText("machine.file.write.with.a.long.dotted.action.name"),
    ).toBeVisible();
  });

  it("has no accessibility violations", async () => {
    const user = userEvent.setup();
    const { container } = renderWith(<AuditPanel />);
    await records();
    await screen.findByText("The chain is intact.");
    await expectNoAxeViolations(container);

    const row = await rowFor("agent.edit");
    await user.click(within(row).getByText("Details"));
    await expectNoAxeViolations(container);
  });

  it("has no accessibility violations when the chain is broken", async () => {
    const api = new FakeApi();
    api.verification = { ...fixtures.verification, ok: false, broken_at_seq: 3 };
    const { container } = renderWith(<AuditPanel />, api);
    await records();
    await screen.findByRole("alert");

    await expectNoAxeViolations(container);
  });
});
