// SPDX-License-Identifier: MIT
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

import type { ChartNode, LiveEntry } from "../api/api";
import { useApi } from "../app/api-context";
import { LoadError, Loading } from "../shell/status";
import { Presence } from "./agent-presence";

/** The org chart: who hired whom. Agents only; humans are visitors, not in the chart. */
export function OrgChart({ live }: { live: LiveEntry[] }) {
  const { t } = useTranslation();
  const api = useApi();
  const chart = useQuery({ queryKey: ["chart"], queryFn: () => api.getChart(), retry: false });
  const lines = new Map(live.map((e) => [e.agent, e]));

  if (chart.isPending) return <Loading label={t("society.chart.loading")} />;
  if (chart.isError) return <LoadError error={chart.error} onRetry={() => void chart.refetch()} />;
  return (
    <ul className="chart" aria-label={t("society.chart.label")}>
      {chart.data.map((node) => (
        <ChartBranch key={node.agent} node={node} lines={lines} />
      ))}
    </ul>
  );
}

function ChartBranch({ node, lines }: { node: ChartNode; lines: Map<string, LiveEntry> }) {
  const { t } = useTranslation();
  const state = lines.get(node.agent)?.state ?? node.state;
  return (
    <li className="chart__node">
      <div className="chart__card">
        <Presence name={node.display_name} state={state} />
        <div>
          <Link className="resident__name" to={`/agents/${encodeURIComponent(node.agent)}/chat`}>
            {node.display_name}
          </Link>
          <p className="resident__meta">
            <span>{node.role}</span>
            <span className={`state state--${state}`}>{t(`agent.state.${state}`)}</span>
            {node.is_root && <span>{t("society.chart.root")}</span>}
          </p>
        </div>
      </div>
      {node.children.length > 0 && (
        <ul className="chart__children">
          {node.children.map((child) => (
            <ChartBranch key={child.agent} node={child} lines={lines} />
          ))}
        </ul>
      )}
    </li>
  );
}
