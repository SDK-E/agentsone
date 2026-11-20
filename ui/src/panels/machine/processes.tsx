// SPDX-License-Identifier: MIT
import { useTranslation } from "react-i18next";

import { useApi } from "../../app/api-context";
import { TimeAgo } from "../shared/time-ago";
import { formatBytes } from "../monitor/format";
import { PagedList } from "./paged-list";

/** Every program running on the machine, busiest first, with who and what owns it. */
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
        renderItem={(p) => (
          <>
            <p className="machine__title code">{p.command}</p>
            <p className="machine__meta">
              <span>{t("pid", { pid: p.pid })}</span>
              <span>{t("by", { user: p.user })}</span>
              {p.agent !== null && <span>{t("agent", { agent: p.agent })}</span>}
              {p.unit !== null && <span>{t("unit", { unit: p.unit })}</span>}
              <span>{t("cpu", { value: p.cpu_percent.toFixed(1) })}</span>
              <span>{t("memory", { value: formatBytes(p.rss_bytes) })}</span>
              <span>
                {t("started")} <TimeAgo iso={p.started_at} />
              </span>
            </p>
          </>
        )}
      />
    </div>
  );
}
