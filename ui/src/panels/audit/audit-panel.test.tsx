// SPDX-License-Identifier: MIT
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoAxeViolations } from "../../test/axe";
import { FakeApi, fixtures, problem, renderWith } from "../../test/fake-api";
import AuditPanel from "./index";

const records = () => screen.findByRole("list", { name: "Records, oldest first" });
const rowFor = async (text: string) => {
  const items = within(await records()).getAllByRole("listitem");
  const row = items.find((li) => within(li).queryByText(text));
  if (!row) throw new Error(`no row with ${text}`);
  return row;
};

describe("AuditPanel chain check", () => {
  it("says the chain is intact and how much of it the edge also holds", async () => {
    renderWith(<AuditPanel />);

    const check = await screen.findByRole("region", { name: "Chain check" });
    expect(await within(check).findByText("The chain is intact.")).toBeVisible();
    expect(within(check).getByText("5 records checked, up to record 5.")).toBeVisible();
    expect(within(check).getByText("The edge holds its own copy up to record 4.")).toBeVisible();
    expect(within(check).getByText("1 newer record has not reached the edge yet.")).toBeVisible();
  });

  it("raises an alarm in plain words when the chain is broken", async () => {
    const api = new FakeApi();
    api.verification = { ...fixtures.verification, ok: false, broken_at_seq: 3 };
    renderWith(<AuditPanel />, api);

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("The chain is broken at record 3.");
    expect(alert).toHaveTextContent("Records from there on may have been changed.");
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
  });
});

describe("AuditPanel records", () => {
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
    api.auditPages = [
      { items: fixtures.audit.filter((r) => r.origin === "inside"), next_cursor: null },
    ];
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
    api.auditPages = [{ items: [], next_cursor: null }];
    renderWith(<AuditPanel />, api);

    expect(await screen.findByRole("heading", { name: "No records yet" })).toBeVisible();
  });

  it("says what went wrong loading records", async () => {
    const api = new FakeApi();
    api.auditPages = problem(500, "Broken", "The log could not be read.");
    renderWith(<AuditPanel />, api);

    const alerts = await screen.findAllByRole("alert");
    expect(alerts.some((a) => a.textContent.includes("The log could not be read."))).toBe(true);
  });

  it("loads the next page on request", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    const [first, second] = fixtures.audit;
    if (!first || !second) throw new Error("fixture needs two records");
    api.auditPages = [
      { items: [first], next_cursor: "1" },
      { items: [second], next_cursor: null },
    ];
    renderWith(<AuditPanel />, api);
    await records();
    expect(screen.queryByText(second.action)).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Show more records" }));

    expect(await screen.findByText(second.action)).toBeVisible();
    expect(screen.queryByRole("button", { name: "Show more records" })).not.toBeInTheDocument();
  });

  it("keeps long actions, targets and details inside the row", async () => {
    const api = new FakeApi();
    const first = fixtures.audit[0];
    if (!first) throw new Error("no fixture");
    api.auditPages = [
      {
        items: [
          {
            ...first,
            action: "machine.file.write.with.a.long.dotted.action.name",
            target: { kind: "file", id: `/home/agents/${"deeply/nested/".repeat(30)}Hero.tsx` },
            details: { error: "Permission denied while writing. ".repeat(20) } as never,
          },
        ],
        next_cursor: null,
      },
    ];
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
});
