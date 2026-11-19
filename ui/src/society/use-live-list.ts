// SPDX-License-Identifier: MIT
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";

import type { LiveEntry, LiveStreamFrame } from "../api/api";
import { useApi } from "../app/api-context";

export const liveKey = ["live"] as const;
/** How often to re-read the live list when the stream is not open. */
export const POLL_MS = 10_000;

/** Fold one stream frame into the list. Changed agents move to the top: most recent first. */
export function applyLiveFrame(list: LiveEntry[], frame: LiveStreamFrame): LiveEntry[] {
  switch (frame.kind) {
    case "entry":
      return [frame.entry, ...list.filter((e) => e.agent !== frame.entry.agent)];
    case "removed":
      return list.filter((e) => e.agent !== frame.agent);
    default:
      return list;
  }
}

export type LiveMode = "stream" | "poll";

/**
 * The live society: every agent's latest one-line. Follows the stream while it is open, and
 * re-reads the list on a timer when it is not.
 */
export function useLiveList() {
  const api = useApi();
  const client = useQueryClient();
  const [mode, setMode] = useState<LiveMode>("stream");
  const [changed, setChanged] = useState<string | null>(null);

  const query = useQuery({
    queryKey: liveKey,
    queryFn: () => api.listLiveList(),
    retry: false,
    refetchInterval: mode === "poll" ? POLL_MS : false,
  });

  useEffect(() => {
    return api.streamLiveList({
      onFrame: (frame) => {
        if (frame.kind === "heartbeat") return;
        client.setQueryData<LiveEntry[]>(liveKey, (old) => applyLiveFrame(old ?? [], frame));
        if (frame.kind === "entry") setChanged(frame.entry.agent);
      },
      onClose: () => {
        setMode("poll");
      },
    });
  }, [api, client]);

  return { ...query, mode, changed };
}
