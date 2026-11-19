// SPDX-License-Identifier: MIT
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import type { Schedule } from "../../api/api";
import { useApi } from "../../app/api-context";
import { LoadError, Loading } from "../../shell/status";
import { relativeTime, useNow } from "../../society/relative-time";
import { describeCalendar } from "./calendar-words";

/** The agents' own timers: when each wakes its agent, and with what message. Read only here. */
export function Schedules({ nameOf }: { nameOf: (agent: string | null) => string | null }) {
  const { t } = useTranslation();
  const api = useApi();
  const schedules = useQuery({
    queryKey: ["schedules"],
    queryFn: () => api.listSchedules(),
    retry: false,
  });
  const now = useNow();
  const items = schedules.data ?? [];

  return (
    <div className="stack">
      {schedules.isPending && <Loading label={t("panels.tasks.schedules.loading")} />}
      {schedules.isError && (
        <LoadError error={schedules.error} onRetry={() => void schedules.refetch()} />
      )}
      {schedules.isSuccess && items.length === 0 && (
        <div className="empty">
          <h3>{t("panels.tasks.schedules.empty.title")}</h3>
          <p>{t("panels.tasks.schedules.empty.body")}</p>
        </div>
      )}
      {schedules.isSuccess && items.length > 0 && (
        <ul className="task-rows" aria-label={t("panels.tasks.schedules.label")}>
          {items.map((s) => (
            <ScheduleRow key={s.id} schedule={s} agent={nameOf(s.agent) ?? s.agent} now={now} />
          ))}
        </ul>
      )}
    </div>
  );
}

function ScheduleRow({ schedule, agent, now }: { schedule: Schedule; agent: string; now: number }) {
  const { t } = useTranslation();
  const words = describeCalendar(schedule.calendar);
  const when =
    words.kind === "hourly"
      ? t("panels.tasks.schedules.hourly")
      : words.kind === "daily"
        ? t("panels.tasks.schedules.daily", { time: words.time })
        : words.kind === "weekly"
          ? t("panels.tasks.schedules.weekly", {
              day: t(`panels.tasks.schedules.day.${words.day}`),
              time: words.time,
            })
          : null;

  return (
    <li className="task-row">
      <p className="task-row__head">
        <span className="task-row__title task-row__title--plain">{schedule.name}</span>
        <span className={`status status--${schedule.enabled ? "on" : "paused"}`}>
          {schedule.enabled ? t("panels.tasks.schedules.on") : t("panels.tasks.schedules.paused")}
        </span>
      </p>
      <p className="task-row__meta">
        {when === null ? <code className="code">{schedule.calendar}</code> : <span>{when}</span>}
        <span>{t("panels.tasks.schedules.wakes", { name: agent })}</span>
      </p>
      <p className="task-row__meta">
        <span>
          {schedule.next_run_at === null
            ? t("panels.tasks.schedules.noNext")
            : t("panels.tasks.schedules.next", { when: relativeTime(schedule.next_run_at, now) })}
        </span>
        <span>
          {schedule.last_run_at === null
            ? t("panels.tasks.schedules.neverRan")
            : t("panels.tasks.schedules.last", { when: relativeTime(schedule.last_run_at, now) })}
        </span>
      </p>
      {schedule.message !== "" && <p className="task-row__text">{schedule.message}</p>}
    </li>
  );
}
