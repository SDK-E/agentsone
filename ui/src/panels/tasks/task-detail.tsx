// SPDX-License-Identifier: MIT
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import type { Task } from "../../api/api";
import { useApi } from "../../app/api-context";
import { LoadError } from "../../shell/status";

const when = (iso: string) =>
  new Intl.DateTimeFormat("en", { dateStyle: "medium", timeStyle: "short" }).format(new Date(iso));

interface TaskDetailProps {
  /** What the list already knows; shown at once while the task is read again. */
  task: Task;
  nameOf: (agent: string | null) => string | null;
  onBack: () => void;
}

/** One task in full: its wording, who holds it, how it started and how it ended. */
export function TaskDetail({ task: listed, nameOf, onBack }: TaskDetailProps) {
  const { t } = useTranslation();
  const api = useApi();
  const query = useQuery({
    queryKey: ["task", listed.id],
    queryFn: () => api.getTask(listed.id),
    initialData: listed,
    retry: false,
  });
  const task = query.data;
  const holder = nameOf(task.assignee);
  const [kind, name] = task.created_by.split(":");

  return (
    <div className="stack">
      <button type="button" className="button button--quiet back-button" onClick={onBack}>
        {t("panels.tasks.back")}
      </button>
      <section aria-labelledby="task-title" className="card stack-sm">
        <div className="card__head">
          <h3 id="task-title">{task.title}</h3>
          <span className={`status status--${task.status}`}>
            {t(`panels.tasks.status.${task.status}`)}
          </span>
        </div>
        {query.isError && <LoadError error={query.error} onRetry={() => void query.refetch()} />}
        {task.detail !== "" && <p className="prose">{task.detail}</p>}
        {task.result !== null && (
          <div className="outcome stack-sm">
            <h4>{t("panels.tasks.outcome")}</h4>
            <p className="prose">{task.result}</p>
          </div>
        )}
        <dl className="facts">
          <div>
            <dt>{t("panels.tasks.filter.heldBy")}</dt>
            <dd>{holder ?? t("panels.tasks.holder.nobody")}</dd>
          </div>
          <div>
            <dt>{t("panels.tasks.priorityLabel")}</dt>
            <dd>{t(`panels.tasks.priority.${task.priority}`)}</dd>
          </div>
          <div>
            <dt>{t("panels.tasks.startedBy")}</dt>
            <dd>
              {t(`panels.tasks.source.${task.source}`)}
              {name !== undefined && kind !== "system" && ` (${nameOf(name) ?? name})`}
            </dd>
          </div>
          {task.due_at !== null && (
            <div>
              <dt>{t("panels.tasks.due")}</dt>
              <dd>{when(task.due_at)}</dd>
            </div>
          )}
          <div>
            <dt>{t("panels.tasks.created")}</dt>
            <dd>{when(task.created_at)}</dd>
          </div>
          <div>
            <dt>{t("panels.tasks.changed")}</dt>
            <dd>{when(task.updated_at)}</dd>
          </div>
        </dl>
      </section>
    </div>
  );
}
