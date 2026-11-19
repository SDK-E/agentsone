// SPDX-License-Identifier: MIT
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

import type { BudgetNode, BudgetSplit, LiveEntry } from "../../api/api";
import { useApi } from "../../app/api-context";
import { LoadError, Loading } from "../../shell/status";
import { Presence } from "../../society/agent-presence";
import { relativeTime, useNow } from "../../society/relative-time";
import { StateReason } from "../../society/state-reason";
import { useLiveList } from "../../society/use-live-list";
import { useStateReasons } from "../../society/use-state-reasons";
import { formatMoney } from "./format";
import { Meter, type MeterTone } from "./meter";

export const splitKey = ["monitor", "split"] as const;

/** Every agent now: its state, what it says it is doing, and its share of the budget. */
export function AgentRows() {
  const { t } = useTranslation();
  const api = useApi();
  const live = useLiveList();
  const split = useQuery({
    queryKey: splitKey,
    queryFn: () => api.getBudgetSplit(),
    retry: false,
    refetchInterval: 10_000,
  });
  const now = useNow();
  const entries = live.data ?? [];
  const reasons = useStateReasons(entries, live.mode);
  const shares = new Map((split.data?.nodes ?? []).map((n) => [n.agent, n]));

  return (
    <section aria-labelledby="monitor-agents" className="card stack-sm">
      <div className="card__head">
        <h3 id="monitor-agents">{t("panels.monitor.agents.title")}</h3>
        <p className={`live-badge live-badge--${live.mode}`}>
          <span className="live-badge__dot" aria-hidden="true" />
          {live.mode === "stream" ? t("society.live") : t("society.polling")}
        </p>
      </div>
      <p className="hint">{t("panels.monitor.agents.softNote")}</p>
      {live.isPending && <Loading label={t("society.loading")} />}
      {live.isError && <LoadError error={live.error} onRetry={() => void live.refetch()} />}
      {live.isSuccess && entries.length === 0 && (
        <div className="empty">
          <h3>{t("panels.monitor.agents.empty.title")}</h3>
          <p>{t("panels.monitor.agents.empty.body")}</p>
        </div>
      )}
      {split.isError && (
        <p className="hint" role="status">
          {t("panels.monitor.agents.splitFailed")}
        </p>
      )}
      {live.isSuccess && entries.length > 0 && (
        <ul className="agent-rows" aria-label={t("panels.monitor.agents.listLabel")}>
          {entries.map((entry) => (
            <AgentRow
              key={entry.agent}
              entry={entry}
              reason={reasons.get(entry.agent)}
              share={shares.get(entry.agent)}
              split={split.data}
              now={now}
              changed={live.changed === entry.agent}
            />
          ))}
        </ul>
      )}
    </section>
  );
}

function AgentRow({
  entry,
  reason,
  share,
  split,
  now,
  changed,
}: {
  entry: LiveEntry;
  reason: string | undefined;
  share: BudgetNode | undefined;
  split: BudgetSplit | undefined;
  now: number;
  changed: boolean;
}) {
  const { t } = useTranslation();
  const line = entry.one_line;
  return (
    <li className={`agent-row${changed ? " agent-row--changed" : ""}`}>
      <Presence name={entry.display_name} state={entry.state} />
      <div className="agent-row__body">
        <p className="agent-row__head">
          <Link
            className="resident__name"
            to={`/agents/${encodeURIComponent(entry.agent)}/chat`}
            aria-label={t("society.openChat", { name: entry.display_name })}
          >
            {entry.display_name}
          </Link>
          <span className={`state state--${entry.state}`}>{t(`agent.state.${entry.state}`)}</span>
        </p>
        <p className="agent-row__line">{line ? line.text : t("society.noLine")}</p>
        <StateReason reason={reason} />
        {line && <p className="hint">{relativeTime(line.updated_at, now)}</p>}
        {share && split && (
          <Share share={share} currency={split.currency} name={entry.display_name} />
        )}
      </div>
    </li>
  );
}

function Share({ share, currency, name }: { share: BudgetNode; currency: string; name: string }) {
  const { t } = useTranslation();
  const spent = formatMoney(share.spent_micros, currency);
  const tone: MeterTone =
    share.state === "exhausted" ? "full" : share.state === "low" ? "low" : "ok";
  return (
    <div className="share">
      {share.allocated_micros === null ? (
        <p className="share__text">{t("panels.monitor.agents.noShare", { spent })}</p>
      ) : (
        <>
          <p className="share__text">
            {t("panels.monitor.agents.used", {
              spent,
              allocated: formatMoney(share.allocated_micros, currency),
            })}
            <span className="wall wall--soft">{t("panels.monitor.agents.soft")}</span>
            {share.state !== "ok" && (
              <span className={`share__state share__state--${tone}`}>
                {t(`panels.monitor.agents.shareState.${share.state}`)}
              </span>
            )}
          </p>
          <Meter
            label={t("panels.monitor.agents.meter", { name })}
            value={share.spent_micros}
            max={share.allocated_micros}
            valueText={spent}
            tone={tone}
          />
        </>
      )}
    </div>
  );
}
