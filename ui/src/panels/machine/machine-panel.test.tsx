// SPDX-License-Identifier: MIT
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoAxeViolations } from "../../test/axe";
import { FakeApi, fixtures, problem, renderWith } from "../../test/fake-api";
import { parentOf } from "./files";
import MachinePanel from ".";

const show = async (section: string) => {
  await userEvent.setup().click(screen.getByRole("radio", { name: section }));
};

describe("Machine panel", () => {
  it("opens on usage, in words and meters", async () => {
    const { container } = renderWith(<MachinePanel />);

    const machine = await screen.findByRole("region", { name: "The machine" });
    expect(await within(machine).findByText("11.5% of 4 cores")).toBeVisible();
    expect(screen.getByRole("radio", { name: "Usage" })).toBeChecked();
    await expectNoAxeViolations(container);
  });

  it("lists processes with who owns them and what they use", async () => {
    const { container } = renderWith(<MachinePanel />);
    await show("Processes");

    const list = await screen.findByRole("list", { name: "Processes" });
    const items = within(list).getAllByRole("listitem");
    expect(items).toHaveLength(fixtures.processes.length);
    const founder = items.find((li) => li.textContent.includes("--agent founder"));
    if (!founder) throw new Error("no founder process");
    expect(within(founder).getByText("Agent founder")).toBeVisible();
    expect(within(founder).getByText("Service agent@founder.service")).toBeVisible();
    expect(within(founder).getByText("3.2% processor")).toBeVisible();
    expect(within(founder).getByText("240.0 MiB memory")).toBeVisible();
    await expectNoAxeViolations(container);
  });

  it("names each service state in words", async () => {
    renderWith(<MachinePanel />);
    await show("Services");

    const list = await screen.findByRole("list", { name: "Services" });
    expect(within(list).getAllByText("Running").length).toBeGreaterThan(0);
    expect(within(list).getByText("Stopped")).toBeVisible();
    expect(within(list).getByText("Failed")).toBeVisible();
    expect(within(list).getByText("Agent keeper")).toBeVisible();
  });

  it("finds packages by name and pages through the rest", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.pageSize = 2;
    renderWith(<MachinePanel />, api);
    await show("Packages");

    const list = await screen.findByRole("list", { name: "Installed packages" });
    expect(within(list).getAllByRole("listitem")).toHaveLength(2);
    await user.click(screen.getByRole("button", { name: "Show more" }));
    expect(await within(list).findByText("nodejs")).toBeVisible();

    await user.type(screen.getByRole("searchbox", { name: "Find a package" }), "git");
    await user.click(screen.getByRole("button", { name: "Find" }));
    expect(await screen.findByText("Packages with “git” in the name")).toBeVisible();
    expect(api.calls).toContain("listPackages git");
    const found = await screen.findByRole("list", { name: "Installed packages" });
    expect(within(found).getAllByRole("listitem")).toHaveLength(1);

    await user.click(screen.getByRole("button", { name: "Clear" }));
    expect(screen.queryByText(/in the name/)).toBeNull();
  });

  it("says when no package matches", async () => {
    const api = new FakeApi();
    api.packages = [];
    renderWith(<MachinePanel />, api);
    await show("Packages");

    expect(await screen.findByRole("heading", { name: "No packages found" })).toBeVisible();
  });

  it("tells people, agents and system accounts apart", async () => {
    renderWith(<MachinePanel />);
    await show("Users");

    const list = await screen.findByRole("list", { name: "Linux users" });
    expect(within(list).getByText("Person")).toBeVisible();
    expect(within(list).getAllByText("Agent")).toHaveLength(3);
    expect(within(list).getByText("System")).toBeVisible();
  });

  it("explains a load failure and offers to retry", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.machineUsers = problem(503, "Unavailable", "Machine control is starting.");
    renderWith(<MachinePanel />, api);
    await show("Users");

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Machine control is starting.");
    api.machineUsers = [];
    await user.click(within(alert).getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("heading", { name: "No users to show" })).toBeVisible();
  });
});

describe("Machine panel, files", () => {
  it("browses into a folder and back up", async () => {
    const user = userEvent.setup();
    const { api, container } = renderWith(<MachinePanel />);
    await show("Files");

    expect(await screen.findByText("In /home/agents/builder/site")).toBeVisible();
    // The field shows the folder the daemon listed, not only the one asked for.
    expect(screen.getByRole("textbox", { name: "Folder" })).toHaveValue(
      "/home/agents/builder/site",
    );
    const list = screen.getByRole("list", { name: "Entries in this folder, folders first" });
    expect(within(list).getByText("Link")).toBeVisible();
    await user.click(within(list).getByRole("button", { name: "Open folder dist" }));
    expect(api.calls).toContain("listFiles /home/agents/builder/site/dist");
    await user.click(await screen.findByRole("button", { name: "Up one folder" }));
    expect(api.calls).toContain("listFiles /home/agents/builder");
    await expectNoAxeViolations(container);
  });

  it("opens a typed path", async () => {
    const user = userEvent.setup();
    const { api } = renderWith(<MachinePanel />);
    await show("Files");

    const field = await screen.findByRole("textbox", { name: "Folder" });
    await user.clear(field);
    await user.type(field, "/etc");
    await user.click(screen.getByRole("button", { name: "Open" }));
    expect(api.calls).toContain("listFiles /etc");
  });

  it("reads a text file and closes it", async () => {
    const user = userEvent.setup();
    renderWith(<MachinePanel />);
    await show("Files");

    await user.click(await screen.findByRole("button", { name: "Read file README.md" }));
    const view = await screen.findByRole("region", { name: "/home/agents/builder/site/README.md" });
    expect(await within(view).findByText(/Built by builder for the society/)).toBeVisible();
    expect(within(view).getByRole("heading")).toHaveFocus();
    await user.click(within(view).getByRole("button", { name: "Close the file" }));
    expect(screen.queryByRole("region", { name: /README/ })).toBeNull();
  });

  it("says why a file cannot be read", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.file = problem(413, "Payload too large", "reads stop at 8 MiB.");
    renderWith(<MachinePanel />, api);
    await show("Files");

    await user.click(await screen.findByRole("button", { name: "Read file README.md" }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("This file could not be read.");
    expect(alert).toHaveTextContent("reads stop at 8 MiB.");
  });

  it("does not show binary or cut files as if they were whole text", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.file = { ...fixtures.file, encoding: "base64", content: "AAEC", size_bytes: 2048 };
    renderWith(<MachinePanel />, api);
    await show("Files");
    await user.click(await screen.findByRole("button", { name: "Read file README.md" }));
    expect(await screen.findByText(/This file is not text/)).toHaveTextContent("2.0 KiB");

    await user.click(screen.getByRole("button", { name: "Close the file" }));
    api.file = { ...fixtures.file, truncated: true };
    await user.click(screen.getByRole("button", { name: "Read file package.json" }));
    expect(await screen.findByText(/Only the first 8 MiB is shown/)).toBeVisible();
  });

  it("finds the folder above a path", () => {
    expect(parentOf("/home/ada")).toBe("/home");
    expect(parentOf("/home/ada/")).toBe("/home");
    expect(parentOf("/home")).toBe("/");
    expect(parentOf("/")).toBe("/");
  });
});

describe("Machine panel, terminals", () => {
  it("lists sessions, opens one and closes one", async () => {
    const user = userEvent.setup();
    const { api, container } = renderWith(<MachinePanel />);
    await show("Terminals");

    const list = await screen.findByRole("list", { name: "Terminal sessions" });
    expect(within(list).getByText("Ended with code 0")).toBeVisible();
    await expectNoAxeViolations(container);

    await user.click(screen.getByRole("button", { name: "Open a terminal" }));
    expect(await screen.findByRole("status")).toHaveTextContent("A terminal is open for ada.");
    expect(api.calls).toContain("createPtySession");

    const [close] = await screen.findAllByRole("button", { name: "Close terminal /bin/bash -l" });
    if (!close) throw new Error("no close button");
    await user.click(close);
    expect(await screen.findByText("The terminal is closed.")).toBeVisible();
    expect(api.calls).toContain(`closePtySession ${fixtures.newPty.id}`);
  });

  it("says when a terminal cannot open", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.ptys = [];
    renderWith(<MachinePanel />, api);
    await show("Terminals");

    expect(await screen.findByRole("heading", { name: "No terminals open" })).toBeVisible();
    api.mutationError = problem(429, "Too many", "You have 8 terminals open.");
    await user.click(screen.getByRole("button", { name: "Open a terminal" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "The terminal could not open. You have 8 terminals open.",
    );
  });

  it("says when a terminal cannot close", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    renderWith(<MachinePanel />, api);
    await show("Terminals");

    api.mutationError = problem(403, "Forbidden");
    await user.click(await screen.findByRole("button", { name: "Close terminal /bin/bash -l" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("The terminal could not close.");
  });
});
