// SPDX-License-Identifier: MIT
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useId, useState, type SyntheticEvent } from "react";
import { useTranslation } from "react-i18next";

import { ApiError, type LiveEntry, type Task, type TaskCreate } from "../../api/api";
import { useApi } from "../../app/api-context";
import { tasksKey } from "./task-cache";

interface TaskFormProps {
  agents: LiveEntry[];
  onCreated: (task: Task) => void;
  onCancel: () => void;
}

/** What the daemon said went wrong: the status, and its own explanation when it sent one. */
function failure(error: unknown): { status: number; detail: string | undefined } {
  if (!(error instanceof ApiError)) return { status: 0, detail: undefined };
  return { status: error.status, detail: error.problem?.detail ?? undefined };
}

/** Make a task. Giving it to an agent wakes that agent. Refusals are shown in plain words. */
export function TaskForm({ agents, onCreated, onCancel }: TaskFormProps) {
  const { t } = useTranslation();
  const api = useApi();
  const client = useQueryClient();
  const id = useId();
  const [title, setTitle] = useState("");
  const [detail, setDetail] = useState("");
  const [priority, setPriority] = useState<NonNullable<TaskCreate["priority"]>>("normal");
  const [assignee, setAssignee] = useState("");
  const [missingTitle, setMissingTitle] = useState(false);

  const create = useMutation({
    mutationFn: (body: TaskCreate) => api.createTask(body),
    onSuccess: async (task) => {
      await client.invalidateQueries({ queryKey: tasksKey });
      onCreated(task);
    },
  });

  const submit = (event: SyntheticEvent) => {
    event.preventDefault();
    if (title.trim() === "") {
      setMissingTitle(true);
      return;
    }
    setMissingTitle(false);
    create.mutate({
      title: title.trim(),
      ...(detail.trim() !== "" && { detail: detail.trim() }),
      priority,
      ...(assignee !== "" && { assignee }),
    });
  };

  const problem = create.isError ? failure(create.error) : undefined;
  const lead =
    problem?.status === 403
      ? t("panels.tasks.form.forbidden")
      : problem?.status === 423
        ? t("panels.tasks.form.frozen")
        : t("panels.tasks.form.failed");

  return (
    <form aria-label={t("panels.tasks.new")} className="card stack" onSubmit={submit} noValidate>
      <h3>{t("panels.tasks.new")}</h3>
      <div className="field">
        <label htmlFor={`${id}-title`}>{t("panels.tasks.form.title")}</label>
        <input
          id={`${id}-title`}
          type="text"
          maxLength={200}
          value={title}
          aria-invalid={missingTitle}
          aria-describedby={missingTitle ? `${id}-title-error` : undefined}
          onChange={(e) => {
            setTitle(e.target.value);
          }}
        />
        {missingTitle && (
          <small id={`${id}-title-error`} className="field__error">
            {t("panels.tasks.form.titleRule")}
          </small>
        )}
      </div>
      <div className="field">
        <label htmlFor={`${id}-detail`}>{t("panels.tasks.form.detail")}</label>
        <textarea
          id={`${id}-detail`}
          rows={3}
          maxLength={16000}
          value={detail}
          onChange={(e) => {
            setDetail(e.target.value);
          }}
        />
      </div>
      <div className="form-row">
        <div className="field">
          <label htmlFor={`${id}-assignee`}>{t("panels.tasks.form.assignee")}</label>
          <select
            id={`${id}-assignee`}
            value={assignee}
            onChange={(e) => {
              setAssignee(e.target.value);
            }}
          >
            <option value="">{t("panels.tasks.form.nobody")}</option>
            {agents.map((a) => (
              <option key={a.agent} value={a.agent}>
                {a.display_name}
              </option>
            ))}
          </select>
        </div>
        <div className="field">
          <label htmlFor={`${id}-priority`}>{t("panels.tasks.priorityLabel")}</label>
          <select
            id={`${id}-priority`}
            value={priority}
            onChange={(e) => {
              setPriority(e.target.value as typeof priority);
            }}
          >
            {(["low", "normal", "high"] as const).map((p) => (
              <option key={p} value={p}>
                {t(`panels.tasks.priorityName.${p}`)}
              </option>
            ))}
          </select>
        </div>
      </div>
      {problem && (
        <div role="alert" className="notice notice--danger stack-sm">
          <p>{lead}</p>
          {problem.detail !== undefined && <p className="notice__detail">{problem.detail}</p>}
        </div>
      )}
      <div className="row row--end">
        <button type="button" className="button" onClick={onCancel}>
          {t("panels.tasks.form.cancel")}
        </button>
        <button type="submit" className="button button--primary" disabled={create.isPending}>
          {create.isPending ? t("panels.tasks.form.pending") : t("panels.tasks.form.submit")}
        </button>
      </div>
    </form>
  );
}
