// SPDX-License-Identifier: MIT
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { at } from "../../test/at";
import { expectNoAxeViolations } from "../../test/axe";
import { FakeApi, fixtures, problem, renderWith } from "../../test/fake-api";
import PluginsPanel from ".";

const installedList = () => screen.findByRole("list", { name: "Installed plugins" });
const row = async (name: string) => {
  const list = await installedList();
  const item = within(list)
    .getAllByRole("listitem")
    .find((li) => within(li).queryByRole("heading", { name: new RegExp(`^${name}`) }));
  if (!item) throw new Error(`no plugin row for ${name}`);
  return item;
};

describe("Plugins panel, installed", () => {
  it("lists each plugin with its kind, source and who installed it", async () => {
    renderWith(<PluginsPanel />);

    const mcp = await row("sample-mcp");
    expect(within(mcp).getByText("MCP server")).toBeVisible();
    expect(within(mcp).getByText("MCP registry")).toBeVisible();
    expect(within(mcp).getByText(/Installed by agent builder/)).toBeVisible();
    expect(within(mcp).getByText("On")).toBeVisible();
    const seed = await row("small-seed");
    expect(within(seed).getByText("Agents One format")).toBeVisible();
    expect(within(seed).getByText(/Installed by the system/)).toBeVisible();
    const pdf = await row("pdf");
    expect(within(pdf).getByText("Off")).toBeVisible();
    expect(within(pdf).getByText("Agent Skills")).toBeVisible();
  });

  it("marks plugins that agents installed", async () => {
    renderWith(<PluginsPanel />);

    expect(within(await row("sample-mcp")).getByText("Installed by an agent")).toBeVisible();
    expect(within(await row("small-seed")).queryByText("Installed by an agent")).toBeNull();
  });

  it("says plainly when nothing is installed", async () => {
    const api = new FakeApi();
    api.plugins = [];
    renderWith(<PluginsPanel />, api);

    expect(await screen.findByRole("heading", { name: "No plugins installed" })).toBeVisible();
  });

  it("explains a load failure and offers to retry", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.plugins = problem(503, "Unavailable", "The plugin manager is starting.");
    renderWith(<PluginsPanel />, api);

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("The plugin manager is starting.");
    api.plugins = [...fixtures.plugins];
    await user.click(within(alert).getByRole("button", { name: "Try again" }));

    expect(await row("sample-mcp")).toBeVisible();
  });

  it("filters by kind", async () => {
    const user = userEvent.setup();
    renderWith(<PluginsPanel />);
    await row("sample-mcp");

    await user.selectOptions(screen.getByRole("combobox", { name: "Show installed" }), "skill");

    expect(await row("pdf")).toBeVisible();
    expect(within(await installedList()).queryByText("sample-mcp")).toBeNull();
  });

  it("turns a plugin off and on again", async () => {
    const user = userEvent.setup();
    const { api } = renderWith(<PluginsPanel />);
    await row("sample-mcp");

    await user.click(screen.getByRole("button", { name: "Turn off sample-mcp" }));
    expect(await screen.findByRole("button", { name: "Turn on sample-mcp" })).toBeVisible();
    expect(within(await row("sample-mcp")).getByText("Off")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Turn on sample-mcp" }));

    expect(await screen.findByRole("button", { name: "Turn off sample-mcp" })).toBeVisible();
    expect(api.calls).toEqual(
      expect.arrayContaining([
        "disablePlugin plg_01JD7ZK3M00000000000000003",
        "enablePlugin plg_01JD7ZK3M00000000000000003",
      ]),
    );
  });

  it("asks before removing, then removes the plugin", async () => {
    const user = userEvent.setup();
    const { api } = renderWith(<PluginsPanel />);
    await row("sample-mcp");

    await user.click(screen.getByRole("button", { name: "Remove sample-mcp" }));
    expect(screen.getByText(/takes along everything it added/)).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Remove for good" }));

    expect(api.calls).toContain("uninstallPlugin plg_01JD7ZK3M00000000000000003");
    expect(await screen.findByText("sample-mcp was removed")).toBeVisible();
    expect(within(await installedList()).queryByText("sample-mcp")).toBeNull();
  });

  it("can back out of a removal", async () => {
    const user = userEvent.setup();
    const { api } = renderWith(<PluginsPanel />);
    await row("sample-mcp");

    await user.click(screen.getByRole("button", { name: "Remove sample-mcp" }));
    await user.click(screen.getByRole("button", { name: "Keep it" }));

    expect(screen.getByRole("button", { name: "Remove sample-mcp" })).toBeVisible();
    expect(api.calls.filter((c) => c.startsWith("uninstall"))).toEqual([]);
  });

  it("shows the daemon's reason on the plugin when a change fails", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.mutationError = problem(409, "Conflict", "An agent is using this plugin right now.");
    renderWith(<PluginsPanel />, api);
    await row("sample-mcp");

    await user.click(screen.getByRole("button", { name: "Turn off sample-mcp" }));

    const failed = within(await row("sample-mcp")).getByRole("alert");
    expect(failed).toHaveTextContent("An agent is using this plugin right now.");
  });

  it("says in plain words when a plugin is not working", async () => {
    const api = new FakeApi();
    const [first] = fixtures.plugins;
    if (!first) throw new Error("no fixture");
    api.plugins = [{ ...first, health: "failed" }];
    renderWith(<PluginsPanel />, api);

    const seed = await row("small-seed");
    expect(within(seed).getByText("Not working")).toBeVisible();
    expect(within(seed).getByText(/Turn it off and on again/)).toBeVisible();
  });

  it("loads more plugins a page at a time", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.pageSize = 2;
    renderWith(<PluginsPanel />, api);
    await row("small-seed");
    expect(within(await installedList()).queryByText("pdf")).toBeNull();

    await user.click(screen.getByRole("button", { name: "Show more installed plugins" }));

    expect(await row("pdf")).toBeVisible();
  });
});

describe("Plugins panel, find and install", () => {
  it("lists catalog entries and tells which are already installed", async () => {
    renderWith(<PluginsPanel />);

    const results = await screen.findByRole("list", { name: "Plugins you can install" });
    const entries = Array.from(results.children) as HTMLElement[];
    expect(entries).toHaveLength(fixtures.catalog.length);
    expect(within(at(entries, 0)).getByText("Installed")).toBeVisible();
    expect(
      within(at(entries, 2)).getByRole("button", { name: "Install code-review" }),
    ).toBeVisible();
    expect(within(at(entries, 2)).getByText("Claude Code format")).toBeVisible();
  });

  it("installs a plugin and shows it in the installed list", async () => {
    const user = userEvent.setup();
    const { api } = renderWith(<PluginsPanel />);

    await user.click(await screen.findByRole("button", { name: "Install disk-watch-panel" }));

    expect(api.calls).toContain("installPlugin agentsone SDK-E/agentsone/plugins/disk-watch-panel");
    expect(await screen.findByText("disk-watch-panel is installed")).toBeVisible();
    expect(await row("disk-watch-panel")).toBeVisible();
    expect(screen.queryByRole("button", { name: "Install disk-watch-panel" })).toBeNull();
  });

  it("shows why an install failed, in the daemon's words", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.mutationError = problem(503, "Unavailable", "The source did not answer.");
    renderWith(<PluginsPanel />, api);

    await user.click(await screen.findByRole("button", { name: "Install code-review" }));

    expect(await screen.findByText("The source did not answer.")).toBeVisible();
    expect(screen.getByRole("button", { name: "Install code-review" })).toBeEnabled();
  });

  it("searches the catalog by words and by source", async () => {
    const user = userEvent.setup();
    const { api } = renderWith(<PluginsPanel />);
    await screen.findByRole("button", { name: "Install code-review" });

    await user.selectOptions(screen.getByRole("combobox", { name: "Source" }), "agent-skills");
    await user.type(screen.getByRole("searchbox", { name: "Search plugins" }), "pdf{Enter}");

    expect(await screen.findByText("anthropics/skills/pdf")).toBeVisible();
    expect(api.calls).toContain("searchPluginCatalog pdf agent-skills");
    expect(screen.queryByRole("button", { name: "Install code-review" })).toBeNull();
  });

  it("offers the four sources by their plain names", async () => {
    renderWith(<PluginsPanel />);

    const sources = await screen.findByRole("combobox", { name: "Source" });
    expect(
      within(sources)
        .getAllByRole("option")
        .map((o) => o.textContent),
    ).toEqual([
      "All sources",
      "Claude Code format",
      "Agents One format",
      "Agent Skills",
      "MCP registry",
    ]);
  });

  it("says when nothing matches", async () => {
    const api = new FakeApi();
    api.catalog = [];
    renderWith(<PluginsPanel />, api);

    expect(await screen.findByRole("heading", { name: "Nothing to install" })).toBeVisible();
  });

  it("explains an unreachable catalog and where the internet rules live", async () => {
    const api = new FakeApi();
    api.catalog = problem(503, "Unavailable", "The MCP registry did not answer.");
    renderWith(<PluginsPanel />, api);

    const alert = await screen.findByText("The MCP registry did not answer.");
    expect(alert).toBeVisible();
    expect(screen.getByText(/internet allow list/)).toBeVisible();
    expect(screen.getByText(/control page/)).toBeVisible();
  });

  it("copes with long names and summaries", async () => {
    const api = new FakeApi();
    const [first] = fixtures.catalog;
    if (!first) throw new Error("no fixture");
    const long = "x".repeat(300);
    api.catalog = [{ ...first, name: `long-${long}`, summary: long, installed: false }];
    const { container } = renderWith(<PluginsPanel />, api);

    expect(await screen.findByRole("button", { name: /^Install long-/ })).toBeVisible();
    await expectNoAxeViolations(container);
  });

  it("has no accessibility violations", async () => {
    const { container } = renderWith(<PluginsPanel />);
    await screen.findByRole("button", { name: "Install code-review" });
    await row("sample-mcp");

    await expectNoAxeViolations(container);
  });
});
