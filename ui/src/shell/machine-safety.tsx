// SPDX-License-Identifier: MIT
//
// The machine safety notice: one shared way to say that something on this machine holds less than
// it should. Each level names what is wrong; the notice and the chip read their words and their
// tone from it. Today there is one level, `limitsSoft`. A stronger level is added as one more
// entry in LEVELS and its words in en.json, without changing the places that show it.
import { useQuery } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";

import type { MachineMetrics } from "../api/api";
import { useApi } from "../app/api-context";
import { usePanelRequest } from "./panel-request";

export const metricsKey = ["monitor", "metrics"] as const;

/** The machine's metrics, refreshed every 10 seconds. Every caller shares one request. */
export function useMachineMetrics() {
  const api = useApi();
  return useQuery({
    queryKey: metricsKey,
    queryFn: () => api.getMachineMetrics(),
    retry: false,
    refetchInterval: 10_000,
  });
}

/** What holds less than it should. `limitsSoft`: the kernel does not hold custom agents' limits. */
export type SafetyLevel = "limitsSoft";

/** How each level is shown. `quiet` is a calm note, never an alert. */
const LEVELS: Record<SafetyLevel, { tone: "quiet"; role: "note" }> = {
  limitsSoft: { tone: "quiet", role: "note" },
};

export interface MachineSafety {
  level: SafetyLevel;
  /** The daemon's own plain words on why, and how to fix it. */
  reason: string | undefined;
}

/**
 * The machine's safety level, or undefined when all holds. A daemon older than contract 1.4.0
 * says nothing about agent limits, and a mode this interface does not know counts as nothing.
 */
export function safetyOf(metrics: MachineMetrics | undefined): MachineSafety | undefined {
  const limits = metrics?.agent_limits;
  if (limits?.mode === "soft") return { level: "limitsSoft", reason: limits.reason };
  return undefined;
}

/**
 * The notice: what holds less than it should, and why in the daemon's words. When the chip asked
 * for it, it brings itself into view and takes focus, once.
 */
export function SafetyNotice({ safety }: { safety: MachineSafety }) {
  const { t } = useTranslation(undefined, { keyPrefix: `safety.${safety.level}` });
  const { tone, role } = LEVELS[safety.level];
  const { request, take } = usePanelRequest();
  const ref = useRef<HTMLDivElement>(null);
  const asked = request?.target === safety.level;
  useEffect(() => {
    if (!asked) return;
    // On a phone the panels sheet opens in the same commit, after this effect, and a modal
    // dialog takes focus as it opens; so move focus on the next frame, once per request.
    const frame = requestAnimationFrame(() => {
      if (!take(safety.level)) return;
      ref.current?.scrollIntoView({ block: "center" });
      ref.current?.focus();
    });
    return () => {
      cancelAnimationFrame(frame);
    };
  }, [asked, take, safety.level]);
  return (
    <div
      ref={ref}
      id={`safety-${safety.level}`}
      tabIndex={-1}
      role={role}
      aria-label={t("label")}
      className={`notice safety safety--${tone} stack-sm`}
    >
      <p>
        <strong>{t("title")}</strong> {t("body")}
      </p>
      {safety.reason && <p className="safety__reason">{safety.reason}</p>}
    </div>
  );
}

/** The chip: small and quiet, for places that only point at the notice. It opens the notice. */
export function SafetyChip({ safety }: { safety: MachineSafety }) {
  const { t } = useTranslation(undefined, { keyPrefix: `safety.${safety.level}` });
  const { tone } = LEVELS[safety.level];
  const { openPanel } = usePanelRequest();
  return (
    <button
      type="button"
      className={`wall wall--soft safety-chip safety-chip--${tone}`}
      aria-label={t("chipLabel")}
      onClick={() => {
        openPanel("machine", safety.level);
      }}
    >
      {t("chip")}
    </button>
  );
}
