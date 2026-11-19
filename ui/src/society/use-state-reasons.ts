// SPDX-License-Identifier: MIT
import { useQueries, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo } from "react";

import type { Agent, LiveEntry } from "../api/api";
import { useApi } from "../app/api-context";
import { POLL_MS, type LiveMode } from "./use-live-list";

export const agentKey = (agent: string) => ["agent", agent] as const;

/**
 * Why each stopped agent stopped, by agent name. The live list carries only the state, so this
 * reads the agent itself, and only for agents that are in the `failed` state: a society of
 * healthy agents costs no extra call. While an agent stays failed, its state frames keep the
 * reason current (a new reason replaces the old one, a frame without one clears it). When the
 * stream is not open, the read repeats on the same timer as the live list.
 */
export function useStateReasons(entries: LiveEntry[], mode: LiveMode): Map<string, string> {
  const api = useApi();
  const client = useQueryClient();
  // Agent names are lowercase words and hyphens, so a newline is a safe separator.
  const key = entries
    .filter((e) => e.state === "failed")
    .map((e) => e.agent)
    .join("\n");
  const failed = useMemo(() => (key === "" ? [] : key.split("\n")), [key]);

  const reads = useQueries({
    queries: failed.map((agent) => ({
      queryKey: agentKey(agent),
      queryFn: () => api.getAgent(agent),
      retry: false,
      refetchInterval: mode === "poll" ? POLL_MS : false,
    })),
  });

  useEffect(() => {
    const stops = failed.map((agent) =>
      api.streamAgentOutput(agent, {
        onFrame: (frame) => {
          if (frame.kind !== "state") return;
          client.setQueryData<Agent>(agentKey(agent), (old) =>
            old ? { ...old, state: frame.state, state_reason: frame.state_reason ?? null } : old,
          );
        },
      }),
    );
    return () => {
      for (const stop of stops) stop();
    };
  }, [api, client, failed]);

  const reasons = new Map<string, string>();
  failed.forEach((agent, i) => {
    const read = reads[i]?.data;
    if (read?.state === "failed" && read.state_reason) reasons.set(agent, read.state_reason);
  });
  return reasons;
}
