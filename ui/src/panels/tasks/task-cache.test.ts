// SPDX-License-Identifier: MIT
import type { InfiniteData } from "@tanstack/react-query";
import { describe, expect, it } from "vitest";

import type { Page, Task } from "../../api/api";
import { fixtures } from "../../test/fake-api";
import { matchesFilter, upsertTask } from "./task-cache";

const [a, b, c] = fixtures.tasks as [Task, Task, Task];
const pages = (...lists: Task[][]): InfiniteData<Page<Task>, string | undefined> => ({
  pages: lists.map((items, i) => ({ items, next_cursor: i < lists.length - 1 ? "next" : null })),
  pageParams: lists.map((_, i) => (i === 0 ? undefined : "next")),
});

describe("matchesFilter", () => {
  it("accepts everything when no filter is set", () => {
    expect(matchesFilter(a, {})).toBe(true);
  });

  it("checks status and assignee together", () => {
    expect(matchesFilter(a, { status: "in-progress", assignee: "scribe" })).toBe(true);
    expect(matchesFilter(a, { status: "done" })).toBe(false);
    expect(matchesFilter(a, { assignee: "builder" })).toBe(false);
  });
});

describe("upsertTask", () => {
  it("replaces a task where it already is", () => {
    const changed = { ...b, status: "open" as const, title: "Renamed" };
    const next = upsertTask(pages([a], [b]), changed, {});
    expect(next.pages[1]?.items[0]?.title).toBe("Renamed");
    expect(next.pages[0]?.items).toEqual([a]);
  });

  it("adds a new task at the top of the first page", () => {
    const next = upsertTask(pages([a]), c, {});
    expect(next.pages[0]?.items.map((t) => t.id)).toEqual([c.id, a.id]);
  });

  it("drops a task that no longer matches the filter", () => {
    const next = upsertTask(pages([a, b]), { ...a, status: "done" }, { status: "in-progress" });
    expect(next.pages[0]?.items.map((t) => t.id)).toEqual([b.id]);
  });

  it("ignores a new task that does not match the filter", () => {
    const next = upsertTask(pages([a]), c, { status: "done" });
    expect(next.pages[0]?.items.map((t) => t.id)).toEqual([a.id]);
  });
});
