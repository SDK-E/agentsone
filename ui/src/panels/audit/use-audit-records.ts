// SPDX-License-Identifier: MIT
import { useInfiniteQuery } from "@tanstack/react-query";

import type { AuditRecord } from "../../api/api";
import { useApi } from "../../app/api-context";

/** How many records one page holds. */
export const AUDIT_PAGE = 25;

interface Window {
  /** The records of one window, newest first. */
  items: AuditRecord[];
}

/**
 * The records newest first, a window at a time. The daemon lists oldest first, so each window is
 * asked for as "after this number", counting back from the newest record the chain check found.
 * The next window ends just below the lowest record of the one before. Windows near the start of
 * the chain can overlap, so the caller drops repeats by sequence number (see `uniqueNewestFirst`).
 */
export function useAuditRecords(newest: number | undefined) {
  const api = useApi();
  return useInfiniteQuery({
    queryKey: ["audit", "records", newest],
    enabled: newest !== undefined,
    initialPageParam: newest ?? 0,
    queryFn: async ({ pageParam }): Promise<Window> => {
      const page = await api.listAuditRecords({
        after_seq: Math.max(0, pageParam - AUDIT_PAGE),
        limit: AUDIT_PAGE,
      });
      return { items: [...page.items].sort((a, b) => b.seq - a.seq) };
    },
    getNextPageParam: (last) => {
      const lowest = last.items.at(-1)?.seq;
      return lowest === undefined || lowest <= 1 ? undefined : lowest - 1;
    },
    retry: false,
  });
}

/** Every record once, newest first, whatever windows they came in. */
export function uniqueNewestFirst(windows: Window[]): AuditRecord[] {
  const seen = new Map<number, AuditRecord>();
  for (const { items } of windows) for (const record of items) seen.set(record.seq, record);
  return [...seen.values()].sort((a, b) => b.seq - a.seq);
}
