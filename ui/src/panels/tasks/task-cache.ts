// SPDX-License-Identifier: MIT
import type { InfiniteData } from "@tanstack/react-query";

import type { Page, Task, TaskFilter } from "../../api/api";

export const tasksKey = ["tasks"] as const;

export type TaskPages = InfiniteData<Page<Task>, string | undefined>;

/** Whether a task belongs in the list the filter describes. */
export function matchesFilter(task: Task, filter: TaskFilter): boolean {
  return (
    (filter.status === undefined || task.status === filter.status) &&
    (filter.assignee === undefined || task.assignee === filter.assignee)
  );
}

/**
 * Fold a task from the stream into loaded pages. A known task changes where it is, or leaves when
 * it no longer fits the filter. A new one that fits goes to the top, since the list is newest first.
 */
export function upsertTask(data: TaskPages, task: Task, filter: TaskFilter): TaskPages {
  const known = data.pages.some((page) => page.items.some((t) => t.id === task.id));
  if (known) {
    return {
      ...data,
      pages: data.pages.map((page) => ({
        ...page,
        items: page.items.flatMap((t) => {
          if (t.id !== task.id) return [t];
          return matchesFilter(task, filter) ? [task] : [];
        }),
      })),
    };
  }
  if (!matchesFilter(task, filter)) return data;
  return {
    ...data,
    pages: data.pages.map((page, i) =>
      i === 0 ? { ...page, items: [task, ...page.items] } : page,
    ),
  };
}
