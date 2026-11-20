// SPDX-License-Identifier: MIT
import { useTranslation } from "react-i18next";

import type { MachineMetrics } from "../../api/api";
import { SafetyNotice, safetyOf, useMachineMetrics } from "../../shell/machine-safety";
import { LoadError, Loading } from "../../shell/status";
import { duration, formatBytes } from "./format";
import { Meter, type MeterTone } from "./meter";

const toneOf = (used: number, total: number): MeterTone => {
  const share = total > 0 ? used / total : 0;
  return share >= 0.95 ? "full" : share >= 0.8 ? "low" : "ok";
};

/**
 * How hard the machine is working: processor, memory, each disk, and how long it has been up.
 * With `limits` (the Machine panel), also whether the kernel holds custom agents' limits.
 */
export function MachineCard({ limits = false }: { limits?: boolean }) {
  const { t } = useTranslation();
  const metrics = useMachineMetrics();
  const data = metrics.data;
  const up = data ? duration(data.uptime_seconds) : undefined;

  return (
    <section aria-labelledby="monitor-machine" className="card stack-sm">
      <h3 id="monitor-machine">{t("panels.monitor.machine.title")}</h3>
      {metrics.isPending && <Loading />}
      {metrics.isError && (
        <LoadError error={metrics.error} onRetry={() => void metrics.refetch()} />
      )}
      {data && up && (
        <>
          <dl className="gauges">
            <Gauge
              name={t("panels.monitor.machine.cpu")}
              text={t("panels.monitor.machine.cpuValue", {
                used: data.cpu.used_percent.toFixed(1),
                count: data.cpu.cores,
              })}
              value={data.cpu.used_percent}
              max={100}
            />
            <Gauge
              name={t("panels.monitor.machine.memory")}
              text={t("panels.monitor.machine.of", {
                used: formatBytes(data.memory.used_bytes),
                total: formatBytes(data.memory.total_bytes),
              })}
              value={data.memory.used_bytes}
              max={data.memory.total_bytes}
            />
            {data.disks.map((disk) => (
              <Gauge
                key={disk.mount}
                name={t("panels.monitor.machine.disk", { mount: disk.mount })}
                text={t("panels.monitor.machine.of", {
                  used: formatBytes(disk.used_bytes),
                  total: formatBytes(disk.total_bytes),
                })}
                value={disk.used_bytes}
                max={disk.total_bytes}
              />
            ))}
          </dl>
          <p className="hint">
            {t("panels.monitor.machine.up", {
              time: [
                up.days > 0 ? t("panels.monitor.machine.days", { count: up.days }) : "",
                up.days > 0 || up.hours > 0
                  ? t("panels.monitor.machine.hours", { count: up.hours })
                  : "",
                t("panels.monitor.machine.minutes", { count: up.minutes }),
              ]
                .filter(Boolean)
                .join(" "),
            })}
            {" · "}
            {t("panels.monitor.machine.load", {
              values: data.load.map((n) => n.toFixed(2)).join(" · "),
            })}
          </p>
          {limits && <AgentLimits metrics={data} />}
        </>
      )}
    </section>
  );
}

/** Soft limits get the safety notice; hard ones one quiet line; an older daemon nothing. */
function AgentLimits({ metrics }: { metrics: MachineMetrics }) {
  const { t } = useTranslation();
  const safety = safetyOf(metrics);
  if (safety) return <SafetyNotice safety={safety} />;
  if (metrics.agent_limits?.mode === "hard")
    return <p className="hint">{t("safety.limitsHard")}</p>;
  return null;
}

function Gauge({
  name,
  text,
  value,
  max,
}: {
  name: string;
  text: string;
  value: number;
  max: number;
}) {
  const { t } = useTranslation();
  const tone = toneOf(value, max);
  // A warning is said in words too, never by the bar's colour or stripes alone.
  const warning = tone === "ok" ? undefined : t(`panels.monitor.machine.tone.${tone}`);
  return (
    <div className="gauge">
      <dt>{name}</dt>
      <dd>
        <span className="gauge__text">{text}</span>
        {warning !== undefined && <span className="gauge__warn">{warning}</span>}
        <Meter
          label={name}
          value={value}
          max={max}
          valueText={warning === undefined ? text : `${text}, ${warning}`}
          tone={tone}
        />
      </dd>
    </div>
  );
}
