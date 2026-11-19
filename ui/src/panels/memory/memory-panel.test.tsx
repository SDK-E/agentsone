// SPDX-License-Identifier: MIT
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { ApiError } from "../../api/api";
import { at } from "../../test/at";
import { expectNoAxeViolations } from "../../test/axe";
import { FakeApi, fixtures, problem, renderWith } from "../../test/fake-api";
import MemoryPanel from ".";

const note = (title: RegExp | string) => screen.findByRole("button", { name: title });

describe("Memory panel", () => {
  it("lists the notes with who wrote each and who can read it", async () => {
    renderWith(<MemoryPanel />);

    const list = await screen.findByRole("list", { name: "Memory notes, newest first" });
    const rows = within(list).getAllByRole("listitem");
    expect(rows).toHaveLength(fixtures.memory.length);
    expect(within(at(rows, 1)).getByText("Site build steps")).toBeVisible();
    expect(within(at(rows, 1)).getByText(/Written by builder/)).toBeVisible();
    expect(within(at(rows, 1)).getByText("Team")).toBeVisible();
    expect(within(at(rows, 3)).getByText("Private")).toBeVisible();
  });

  it("says plainly when nothing is remembered yet", async () => {
    const api = new FakeApi();
    api.memory = [];
    renderWith(<MemoryPanel />, api);

    expect(await screen.findByRole("heading", { name: "Nothing remembered yet" })).toBeVisible();
  });

  it("explains a load failure in the daemon's words and offers to retry", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.memory = problem(503, "Unavailable", "Memory is starting up.");
    renderWith(<MemoryPanel />, api);

    expect(await screen.findByRole("alert")).toHaveTextContent("Memory is starting up.");
    api.memory = [...fixtures.memory];
    await user.click(screen.getByRole("button", { name: "Try again" }));

    expect(await note(/Site build steps/)).toBeVisible();
  });

  it("filters by who can read the notes", async () => {
    const user = userEvent.setup();
    renderWith(<MemoryPanel />);
    await note(/Site build steps/);

    await user.click(screen.getByRole("radio", { name: "Team" }));

    expect(await note(/Site build steps/)).toBeVisible();
    expect(screen.queryByRole("button", { name: /Journal vacuum notes/ })).toBeNull();
  });

  it("opens a note with its text, tags, links and the writer", async () => {
    const user = userEvent.setup();
    renderWith(<MemoryPanel />);

    await user.click(await note(/Site build steps/));

    const detail = screen.getByRole("region", { name: "Site build steps" });
    expect(within(detail).getByText("pnpm install")).toBeVisible();
    expect(within(detail).getByText("site")).toBeVisible();
    expect(within(detail).getByText("howto")).toBeVisible();
    expect(within(detail).getByText("Written by")).toBeVisible();
    expect(within(detail).getByText("builder", { selector: "dd" })).toBeVisible();
    expect(within(detail).getByText("Version 2")).toBeVisible();
    expect(within(detail).getByText(/Task/)).toBeVisible();
    expect(within(detail).getByRole("link", { name: "Ask builder to change it" })).toHaveAttribute(
      "href",
      "/agents/builder/chat",
    );
  });

  it("shows a web link in a note as a link that opens a new tab", async () => {
    const user = userEvent.setup();
    renderWith(<MemoryPanel />);

    await user.click(await note(/How this society works/));

    const link = screen.getByRole("link", { name: "Agents One" });
    expect(link).toHaveAttribute("href", "https://github.com/SDK-E/agentsone");
    expect(link).toHaveAttribute("target", "_blank");
  });

  it("searches and marks the words that matched", async () => {
    const user = userEvent.setup();
    const { api } = renderWith(<MemoryPanel />);
    await note(/Site build steps/);

    await user.type(screen.getByRole("searchbox", { name: "Search memory" }), "disk{Enter}");

    const results = await screen.findByRole("list", { name: "Search results, best match first" });
    expect(api.calls).toContain("searchMemory disk");
    expect(within(results).getAllByRole("listitem")).toHaveLength(fixtures.memoryHits.length);
    expect(results.querySelector("mark")).toHaveTextContent("disk");
  });

  it("says so when a search finds nothing, and clears back to the list", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.memoryHits = [];
    renderWith(<MemoryPanel />, api);
    await note(/Site build steps/);

    await user.type(screen.getByRole("searchbox", { name: "Search memory" }), "zebra{Enter}");

    expect(await screen.findByRole("heading", { name: "Nothing found" })).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Clear search" }));
    expect(await note(/Site build steps/)).toBeVisible();
  });

  it("asks before deleting, then removes the note", async () => {
    const user = userEvent.setup();
    const { api } = renderWith(<MemoryPanel />);
    await user.click(await note(/Journal vacuum notes/));

    await user.click(screen.getByRole("button", { name: "Delete this note" }));
    expect(screen.getByText(/Nobody can bring it back/)).toBeVisible();
    expect(api.calls).not.toContain("deleteMemory mem_01JD7ZK3M00000000000000005");
    await user.click(screen.getByRole("button", { name: "Delete for good" }));

    expect(api.calls).toContain("deleteMemory mem_01JD7ZK3M00000000000000005");
    expect(await screen.findByText("Note deleted")).toBeVisible();
    expect(screen.queryByRole("button", { name: /Journal vacuum notes/ })).toBeNull();
  });

  it("keeps the note and shows the daemon's reason when delete is not allowed", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.mutationError = problem(
      403,
      "Forbidden",
      "Only the writer or a root agent may delete this.",
    );
    renderWith(<MemoryPanel />, api);
    await user.click(await note(/Journal vacuum notes/));

    await user.click(screen.getByRole("button", { name: "Delete this note" }));
    await user.click(screen.getByRole("button", { name: "Delete for good" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Only the writer or a root agent may delete this.",
    );
    expect(screen.getByRole("button", { name: /Journal vacuum notes/ })).toBeVisible();
  });

  it("can back out of a delete", async () => {
    const user = userEvent.setup();
    const { api } = renderWith(<MemoryPanel />);
    await user.click(await note(/Journal vacuum notes/));

    await user.click(screen.getByRole("button", { name: "Delete this note" }));
    await user.click(screen.getByRole("button", { name: "Keep it" }));

    expect(screen.getByRole("button", { name: "Delete this note" })).toBeVisible();
    expect(api.calls.filter((c) => c.startsWith("deleteMemory"))).toEqual([]);
  });

  it("loads more notes a page at a time", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.pageSize = 2;
    renderWith(<MemoryPanel />, api);
    await note(/How this society works/);
    expect(screen.queryByRole("button", { name: /Journal vacuum notes/ })).toBeNull();

    await user.click(screen.getByRole("button", { name: "Show more" }));

    expect(await note(/Journal vacuum notes/)).toBeVisible();
    expect(screen.queryByRole("button", { name: "Show more" })).toBeNull();
  });

  it("copes with very long titles, bodies and tags", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.memory = fixtures.memoryLong;
    const { container } = renderWith(<MemoryPanel />, api);

    await user.click(await note(/A deliberately long memory title/));

    expect(screen.getByText(/a-tag-that-is-thirty-two-chars-x/)).toBeVisible();
    await expectNoAxeViolations(container);
  });

  it("has no accessibility violations in the list or with a note open", async () => {
    const user = userEvent.setup();
    const { container } = renderWith(<MemoryPanel />);
    await user.click(await note(/Site build steps/));

    await expectNoAxeViolations(container);
  });

  it("reports an unexpected delete failure in plain words", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.mutationError = new ApiError(500);
    renderWith(<MemoryPanel />, api);
    await user.click(await note(/Journal vacuum notes/));

    await user.click(screen.getByRole("button", { name: "Delete this note" }));
    await user.click(screen.getByRole("button", { name: "Delete for good" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("The note could not be deleted");
  });
});
