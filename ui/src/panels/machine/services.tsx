// SPDX-License-Identifier: MIT
import { useTranslation } from "react-i18next";

import type { MachineService } from "../../api/api";
import { useApi } from "../../app/api-context";
import { PagedList } from "./paged-list";

const tone = (state: MachineService["active_state"]) =>
  state === "active" ? "status--on" : state === "failed" ? "status--failed" : "status--paused";

/** The services systemd runs, with their state in words. */
export function ServicesSection() {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.machine.services" });
  const api = useApi();
  return (
    <div className="stack-sm">
      <p className="hint">{t("lead")}</p>
      <PagedList
        queryKey={["machine", "services"]}
        fetchPage={(cursor) => api.listServices(cursor)}
        label={t("listLabel")}
        loading={t("loading")}
        empty={{ title: t("empty.title"), body: t("empty.body") }}
        itemKey={(s) => s.unit}
        refetchInterval={10_000}
        renderItem={(s) => (
          <>
            <div className="machine__head">
              <p className="machine__title code">{s.unit}</p>
              <span className={`status ${tone(s.active_state)}`}>
                {t(`state.${s.active_state}`)}
              </span>
            </div>
            <p>{s.description}</p>
            <p className="machine__meta">
              <span>{s.sub_state}</span>
              <span>{s.enabled ? t("enabled") : t("manual")}</span>
              {s.agent !== null && <span>{t("agent", { agent: s.agent })}</span>}
            </p>
          </>
        )}
      />
    </div>
  );
}
