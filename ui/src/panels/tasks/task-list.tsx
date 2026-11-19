// SPDX-License-Identifier: MIT
import { useTranslation } from "react-i18next";

import type { LiveEntry, Task, TaskFilter, TaskStatus } from "../../api/api";
import { LoadError, Loading } from "../../shell/status";
import { relativeTime, useNow } from "../../society/relative-time";
import { useTasks } from "./use-tasks";

export const STATUSES: readonly TaskStatus[] = [
  "open",
  "in-progress",
  "blocked",
  "done",
  "cancelled",
  "failed",
];

interface TaskListProps {
  filter: TaskFilter;
  onFilter: (filter: TaskFilter) => void;
  agents: LiveEntry[];
  nameOf: (agent: string | null) => string | null;
  onOpen: (task: Task) => void;
}

/** Filters, then the tasks that fit them: newest first, a page at a time. */
export function TaskList({ filter, onFilter, agents, nameOf, onOpen }: TaskListProps) {
  const { t } = useTranslation();
  const tasks = useTasks(filter);
  const now = useNow();
  const items = tasks.data?.pages.flatMap((page) => page.items) ?? [];
  const filtered = filter.status !== undefined || filter.assignee !== undefined;

  const withStatus = (value: string): TaskFilter => ({
    ...(value !== "" && { status: value as TaskStatus }),
    ...(filter.assignee !== undefined && { assignee: filter.assignee }),
  });
  const withAssignee = (value: string): TaskFilter => ({
    ...(filter.status !== undefined && { status: filter.status }),
    ...(value !== "" && { assignee: value }),
  });

  return (
    <div className="stack">
      <div className="filters">
        <label className="field field--inline">
          <span>{t("panels.tasks.filter.status")}</span>
          <select
            value={filter.status ?? ""}
            onChange={(e) => {
              onFilter(withStatus(e.target.value));
            }}
          >
            <option value="">{t("panels.tasks.filter.anyStatus")}</option>
            {STATUSES.map((s) => (
              <option key={s} value={s}>
                {t(`panels.tasks.status.${s}`)}
              </option>
            ))}
          </select>
        </label>
        <label className="field field--inline">
          <span>{t("panels.tasks.filter.heldBy")}</span>
          <select
            value={filter.assignee ?? ""}
            onChange={(e) => {
              onFilter(withAssignee(e.target.value));
            }}
          >
            <option value="">{t("panels.tasks.filter.anyone")}</option>
            {agents.map((a) => (
              <option key={a.agent} value={a.agent}>
                {a.display_name}
              </option>
            ))}
          </select>
        </label>
      </div>

      {tasks.isPending && <Loading label={t("panels.tasks.loading")} />}
      {tasks.isError && <LoadError error={tasks.error} onRetry={() => void tasks.refetch()} />}
      {tasks.isSuccess && items.length === 0 && (
        <div className="empty">
          {filtered ? (
            <>
              <h3>{t("panels.tasks.noMatch.title")}</h3>
              <p>{t("panels.tasks.noMatch.body")}</p>
              <button
                type="button"
                className="button"
                onClick={() => {
                  onFilter({});
                }}
              >
                {t("panels.tasks.noMatch.clear")}
              </button>
            </>
          ) : (
            <>
              <h3>{t("panels.tasks.empty.title")}</h3>
              <p>{t("panels.tasks.empty.body")}</p>
            </>
          )}
        </div>
      )}
      {tasks.isSuccess && items.length > 0 && (
        <ul className="task-rows" aria-label={t("panels.tasks.listLabel")}>
          {items.map((task) => (
            <TaskRow key={task.id} task={task} nameOf={nameOf} now={now} onOpen={onOpen} />
          ))}
        </ul>
      )}
      {tasks.hasNextPage && (
        <button
          type="button"
          className="button"
          disabled={tasks.isFetchingNextPage}
          onClick={() => void tasks.fetchNextPage()}
        >
          {tasks.isFetchingNextPage ? t("panels.tasks.loadingMore") : t("panels.tasks.more")}
        </button>
      )}
    </div>
  );
}

function TaskRow({
  task,
  nameOf,
  now,
  onOpen,
}: {
  task: Task;
  nameOf: (agent: string | null) => string | null;
  now: number;
  onOpen: (task: Task) => void;
}) {
  const { t } = useTranslation();
  const holder = nameOf(task.assignee);
  return (
    <li className="task-row">
      <p className="task-row__head">
        <button
          type="button"
          className="task-row__title"
          onClick={() => {
            onOpen(task);
          }}
        >
          {task.title}
        </button>
        <span className={`status status--${task.status}`}>
          {t(`panels.tasks.status.${task.status}`)}
        </span>
      </p>
      <p className="task-row__meta">
        <span>{t(`panels.tasks.priority.${task.priority}`)}</span>
        <span>
          {holder === null
            ? t("panels.tasks.holder.nobody")
            : t("panels.tasks.holder.with", { name: holder })}
        </span>
        <span>{t("panels.tasks.updated", { when: relativeTime(task.updated_at, now) })}</span>
      </p>
    </li>
  );
}
