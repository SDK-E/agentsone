// SPDX-License-Identifier: MIT
import { useState } from "react";
import { useTranslation } from "react-i18next";

import type { Task, TaskFilter } from "../../api/api";
import { Schedules } from "./schedules";
import { TaskDetail } from "./task-detail";
import { TaskForm } from "./task-form";
import { TaskList } from "./task-list";
import { useAgents, useTaskStream } from "./use-tasks";

type View = "tasks" | "schedules";

/** Tasks: the work agents gave each other and the work you ask for, plus the agents' timers. */
export default function TasksPanel() {
  const { t } = useTranslation();
  const [view, setView] = useState<View>("tasks");
  const [filter, setFilter] = useState<TaskFilter>({});
  const [open, setOpen] = useState<Task | null>(null);
  const [creating, setCreating] = useState(false);
  const [created, setCreated] = useState(false);
  const { agents, nameOf } = useAgents();
  useTaskStream();

  return (
    <div className="stack">
      <div className="panel__header">
        <div className="toggle" role="group" aria-label={t("panels.tasks.viewLabel")}>
          {(["tasks", "schedules"] as const).map((v) => (
            <button
              key={v}
              type="button"
              className="toggle__option"
              aria-pressed={view === v}
              onClick={() => {
                setView(v);
                setOpen(null);
              }}
            >
              {t(`panels.tasks.view.${v}`)}
            </button>
          ))}
        </div>
        {view === "tasks" && !creating && open === null && (
          <button
            type="button"
            className="button button--primary"
            onClick={() => {
              setCreating(true);
              setCreated(false);
            }}
          >
            {t("panels.tasks.new")}
          </button>
        )}
      </div>

      {created && !creating && (
        <p role="status" className="notice">
          {t("panels.tasks.form.created")}
        </p>
      )}

      {view === "schedules" && <Schedules nameOf={nameOf} />}
      {view === "tasks" && creating && (
        <TaskForm
          agents={agents}
          onCancel={() => {
            setCreating(false);
          }}
          onCreated={() => {
            setCreating(false);
            setCreated(true);
          }}
        />
      )}
      {view === "tasks" && open !== null && (
        <TaskDetail
          task={open}
          nameOf={nameOf}
          onBack={() => {
            setOpen(null);
          }}
        />
      )}
      {view === "tasks" && open === null && (
        <TaskList
          filter={filter}
          onFilter={setFilter}
          agents={agents}
          nameOf={nameOf}
          onOpen={setOpen}
        />
      )}
    </div>
  );
}
