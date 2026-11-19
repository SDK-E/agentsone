// SPDX-License-Identifier: MIT
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";

import type { LiveEntry, TaskFilter } from "../../api/api";
import { useApi } from "../../app/api-context";
import { liveKey } from "../../society/use-live-list";
import { tasksKey, upsertTask, type TaskPages } from "./task-cache";

/** The tasks that fit the filter, a page at a time, newest first. */
export function useTasks(filter: TaskFilter) {
  const api = useApi();
  return useInfiniteQuery({
    queryKey: [...tasksKey, filter],
    queryFn: ({ pageParam }) => api.listTasks(filter, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    retry: false,
  });
}

/** Keep every loaded task list, and open task, true to what the stream reports. */
export function useTaskStream(): void {
  const api = useApi();
  const client = useQueryClient();
  useEffect(() => {
    return api.streamTasks({
      onFrame: (frame) => {
        if (frame.kind !== "task") return;
        for (const [key, data] of client.getQueriesData<TaskPages>({ queryKey: tasksKey })) {
          const filter = key[1] as TaskFilter | undefined;
          if (data && filter) client.setQueryData(key, upsertTask(data, frame.task, filter));
        }
        client.setQueryData(["task", frame.task.id], frame.task);
      },
    });
  }, [api, client]);
}

/**
 * The agents, by name. Shares the live list's cache with the home screen, so it adds no stream.
 */
export function useAgents(): {
  agents: LiveEntry[];
  nameOf: (agent: string | null) => string | null;
} {
  const api = useApi();
  const live = useQuery({ queryKey: liveKey, queryFn: () => api.listLiveList(), retry: false });
  const agents = live.data ?? [];
  return {
    agents,
    nameOf: (agent) =>
      agent === null ? null : (agents.find((e) => e.agent === agent)?.display_name ?? agent),
  };
}
