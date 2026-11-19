// SPDX-License-Identifier: MIT
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { useApi } from "../../app/api-context";
import { LoadError, Loading } from "../../shell/status";
import { formatMoney } from "./format";
import { Meter, type MeterTone } from "./meter";

export const budgetKey = ["monitor", "budget"] as const;

/** The human's total budget. It is a hard limit, held outside the machine. */
export function BudgetCard() {
  const { t } = useTranslation();
  const api = useApi();
  const budget = useQuery({
    queryKey: budgetKey,
    queryFn: () => api.getBudget(),
    retry: false,
    refetchInterval: 30_000,
  });
  const data = budget.data;
  const tone: MeterTone =
    data?.state === "exhausted" ? "full" : data?.state === "low" ? "low" : "ok";

  return (
    <section aria-labelledby="monitor-budget" className="card stack-sm">
      <div className="card__head">
        <h3 id="monitor-budget">{t("panels.monitor.budget.title")}</h3>
        <span className="wall wall--hard">{t("panels.monitor.budget.hard")}</span>
      </div>
      {budget.isPending && <Loading />}
      {budget.isError && <LoadError error={budget.error} onRetry={() => void budget.refetch()} />}
      {data && (
        <>
          <p className="figure">
            {t("panels.monitor.budget.left", {
              left: formatMoney(data.remaining_micros, data.currency),
              total: formatMoney(data.total_micros, data.currency),
            })}
          </p>
          <Meter
            label={t("panels.monitor.budget.meter")}
            value={data.spent_micros}
            max={data.total_micros}
            valueText={t("panels.monitor.budget.spent", {
              spent: formatMoney(data.spent_micros, data.currency),
            })}
            tone={tone}
          />
          {data.state === "exhausted" && (
            <p role="alert" className="notice notice--danger">
              {t("walls.budget")}
            </p>
          )}
          {data.state === "low" && <p className="hint">{t("panels.monitor.budget.low")}</p>}
          <p className="hint">{t("panels.monitor.budget.hardNote")}</p>
          {data.defence.enabled ? (
            <div className="stack-sm">
              <p className="hint">
                {t("panels.monitor.budget.defence", {
                  spent: formatMoney(data.defence.spent_micros, data.currency),
                  reserved: formatMoney(data.defence.reserved_micros, data.currency),
                })}
                <span className="wall wall--soft">{t("panels.monitor.agents.soft")}</span>
              </p>
              <p className="hint">{t("panels.monitor.budget.defenceWhy")}</p>
            </div>
          ) : (
            <p className="hint">{t("panels.monitor.budget.defenceOff")}</p>
          )}
        </>
      )}
    </section>
  );
}
