// SPDX-License-Identifier: MIT
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

import type { LiveEntry } from "../api/api";
import { useStale } from "../shell/stale";
import { LoadError, Loading } from "../shell/status";
import { isAlive, Presence } from "./agent-presence";
import { OrgChart } from "./org-chart";
import { relativeTime, useNow } from "./relative-time";
import { StateReason } from "./state-reason";
import { useLiveList } from "./use-live-list";
import { useStateReasons } from "./use-state-reasons";

type View = "list" | "chart";

/** The live society: who lives here, what each one is doing now, and how they are organised. */
export function LiveSociety() {
  const { t } = useTranslation();
  const live = useLiveList();
  const stale = useStale();
  const [view, setView] = useState<View>("list");
  const now = useNow();
  const entries = live.data ?? [];
  const working = entries.filter((e) => isAlive(e.state)).length;
  const reasons = useStateReasons(entries, live.mode);

  return (
    <section aria-labelledby="society-title" className="panel society">
      <header className="panel__header">
        <div>
          <h2 id="society-title">{t("society.title")}</h2>
          {live.isSuccess && entries.length > 0 && (
            <p className="hint">
              {t("society.summary", { count: entries.length, awake: working })}
            </p>
          )}
        </div>
        <div className="society__tools">
          <p className={`live-badge live-badge--${live.mode}`}>
            <span className="live-badge__dot" aria-hidden="true" />
            {stale
              ? t("society.paused")
              : live.mode === "stream"
                ? t("society.live")
                : t("society.polling")}
          </p>
          <div className="toggle" role="group" aria-label={t("society.viewLabel")}>
            {(["list", "chart"] as const).map((v) => (
              <button
                key={v}
                type="button"
                className="toggle__option"
                aria-pressed={view === v}
                onClick={() => {
                  setView(v);
                }}
              >
                {t(`society.view.${v}`)}
              </button>
            ))}
          </div>
        </div>
      </header>

      {live.isPending && <Loading label={t("society.loading")} />}
      {live.isError && <LoadError error={live.error} onRetry={() => void live.refetch()} />}
      {live.isSuccess && entries.length === 0 && (
        <div className="empty">
          <h3>{t("society.empty.title")}</h3>
          <p>{t("society.empty.body")}</p>
        </div>
      )}
      {live.isSuccess && entries.length > 0 && view === "list" && (
        <ul className="residents" aria-label={t("society.listLabel")}>
          {entries.map((entry) => (
            <Resident
              key={entry.agent}
              entry={entry}
              reason={reasons.get(entry.agent)}
              now={now}
              changed={live.changed === entry.agent}
            />
          ))}
        </ul>
      )}
      {live.isSuccess && entries.length > 0 && view === "chart" && <OrgChart live={entries} />}
    </section>
  );
}

function Resident({
  entry,
  reason,
  now,
  changed,
}: {
  entry: LiveEntry;
  reason: string | undefined;
  now: number;
  changed: boolean;
}) {
  const { t } = useTranslation();
  const line = entry.one_line;
  return (
    <li className={`resident${changed ? " resident--changed" : ""}`}>
      <Presence name={entry.display_name} state={entry.state} />
      <div className="resident__body">
        <p className="resident__head">
          <Link
            className="resident__name"
            to={`/agents/${encodeURIComponent(entry.agent)}/chat`}
            aria-label={t("society.openChat", { name: entry.display_name })}
          >
            {entry.display_name}
          </Link>
          <span className="resident__role">
            {t("society.roleInTeam", { role: entry.role, team: entry.team })}
          </span>
        </p>
        <p className="resident__line">{line ? line.text : t("society.noLine")}</p>
        <StateReason reason={reason} />
        <p className="resident__meta">
          <span className={`state state--${entry.state}`}>{t(`agent.state.${entry.state}`)}</span>
          <span>{t(`agent.kind.${entry.kind}`)}</span>
          {line && <span>{relativeTime(line.updated_at, now)}</span>}
        </p>
      </div>
    </li>
  );
}
