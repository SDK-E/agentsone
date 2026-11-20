// SPDX-License-Identifier: MIT
import { useState } from "react";
import { useTranslation } from "react-i18next";

import type { MachineProcess } from "../../api/api";
import { useApi } from "../../app/api-context";
import { TimeAgo } from "../shared/time-ago";
import { formatBytes } from "../monitor/format";
import { PagedList } from "./paged-list";

/** Commands longer than this may not fit two lines on a phone, so they get a "Show full" toggle. */
const LONG_COMMAND = 80;

/** The program's own name from a command line: "/usr/bin/node app.js" gives "node". */
export function programOf(command: string): string {
  const first = command.trim().split(/\s+/)[0] ?? "";
  const name = first.split("/").pop() ?? "";
  return name === "" ? command : name;
}

/** Every program running on the machine, with who and what owns it. */
export function ProcessesSection() {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.machine.processes" });
  const api = useApi();
  return (
    <div className="stack-sm">
      <p className="hint">{t("lead")}</p>
      <PagedList
        queryKey={["machine", "processes"]}
        fetchPage={(cursor) => api.listProcesses(cursor)}
        label={t("listLabel")}
        loading={t("loading")}
        empty={{ title: t("empty.title"), body: t("empty.body") }}
        itemKey={(p) => String(p.pid)}
        refetchInterval={10_000}
        renderItem={(p) => <ProcessRow process={p} />}
      />
    </div>
  );
}

function ProcessRow({ process: p }: { process: MachineProcess }) {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.machine.processes" });
  const [full, setFull] = useState(false);
  const commandId = `machine-process-${String(p.pid)}`;
  return (
    <>
      <div className="machine__head">
        <p className="machine__title">{programOf(p.command)}</p>
        <span className="machine__figure">{t("cpu", { value: p.cpu_percent.toFixed(1) })}</span>
      </div>
      <p id={commandId} className={`machine__command${full ? "" : " machine__command--clamped"}`}>
        {p.command}
      </p>
      {p.command.length > LONG_COMMAND && (
        <button
          type="button"
          className="machine__toggle"
          aria-expanded={full}
          aria-controls={commandId}
          onClick={() => {
            setFull(!full);
          }}
        >
          {full ? t("lessCommand") : t("fullCommand")}
        </button>
      )}
      <p className="machine__meta">
        <span>{t("pid", { pid: p.pid })}</span>
        <span>{t("by", { user: p.user })}</span>
        {p.agent !== null && <span>{t("agent", { agent: p.agent })}</span>}
        {p.unit !== null && <span>{t("unit", { unit: p.unit })}</span>}
        <span>{t("memory", { value: formatBytes(p.rss_bytes) })}</span>
        <span>
          {t("started")} <TimeAgo iso={p.started_at} />
        </span>
      </p>
    </>
  );
}
