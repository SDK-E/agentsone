// SPDX-License-Identifier: MIT
import { useMutation } from "@tanstack/react-query";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";

import type { Plugin } from "../../api/api";
import { useApi } from "../../app/api-context";
import { problemText } from "../../shell/problem-text";
import { TimeAgo } from "../shared/time-ago";
import { parseActor } from "./labels";

/** Short chips naming what a plugin brings. */
export function KindChips({ kinds }: { kinds: readonly string[] }) {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.plugins" });
  return (
    <ul className="chips">
      {kinds.map((kind) => (
        <li key={kind} className="chip">
          {t(`kind.${kind}`, kind)}
        </li>
      ))}
    </ul>
  );
}

/** One installed plugin: what it is, who installed it, and the changes a person can make. */
export function InstalledRow({
  plugin,
  onChanged,
}: {
  plugin: Plugin;
  onChanged: (message?: string) => void;
}) {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.plugins" });
  const api = useApi();
  const nameId = useId();
  const [confirming, setConfirming] = useState(false);
  const by = parseActor(plugin.installed_by);

  const toggle = useMutation({
    mutationFn: () => (plugin.enabled ? api.disablePlugin(plugin.id) : api.enablePlugin(plugin.id)),
    onSuccess: () => {
      onChanged();
    },
  });
  const remove = useMutation({
    mutationFn: () => api.uninstallPlugin(plugin.id),
    onSuccess: () => {
      onChanged(t("removed", { name: plugin.name }));
    },
  });

  const failure = toggle.isError ? toggle.error : remove.isError ? remove.error : undefined;
  const busy = toggle.isPending || remove.isPending;
  const toggleLabel = plugin.enabled
    ? t("turnOffLabel", { name: plugin.name })
    : t("turnOnLabel", { name: plugin.name });
  const toggleText = toggle.isPending
    ? plugin.enabled
      ? t("turningOff")
      : t("turningOn")
    : plugin.enabled
      ? t("turnOff")
      : t("turnOn");

  return (
    <li className="plugin" aria-labelledby={nameId}>
      <div className="plugin__head">
        <h5 id={nameId} className="plugin__name">
          {plugin.name}
          <span className="plugin__version"> {plugin.version}</span>
        </h5>
        <span className={`plugin__state${plugin.enabled ? " plugin__state--on" : ""}`}>
          {plugin.enabled ? t("on") : t("off")}
        </span>
      </div>

      {plugin.manifest.description !== "" && (
        <p className="plugin__about">{plugin.manifest.description}</p>
      )}

      <KindChips kinds={plugin.kinds} />

      <p className="plugin__meta">
        <span>{t(`source.${plugin.source}`)}</span>
        <span>{t(`by.${by.kind}`, { name: by.name })}</span>
        <TimeAgo iso={plugin.installed_at} />
      </p>
      {by.kind === "agent" && <p className="plugin__agent">{t("byAgent")}</p>}

      {plugin.health !== "ok" && (
        <div className="plugin__health stack-sm">
          <p className="plugin__health-state">{t(`health.${plugin.health}`)}</p>
          <p className="hint">{t(`health.${plugin.health}Advice`)}</p>
        </div>
      )}

      <div className="row plugin__actions">
        <button
          type="button"
          className="button"
          aria-label={toggleLabel}
          disabled={busy}
          onClick={() => {
            toggle.mutate();
          }}
        >
          {toggleText}
        </button>
        {!confirming && (
          <button
            type="button"
            className="button button--danger"
            aria-label={t("removeLabel", { name: plugin.name })}
            disabled={busy}
            onClick={() => {
              setConfirming(true);
            }}
          >
            {t("remove")}
          </button>
        )}
      </div>

      {confirming && (
        <div className="notice stack-sm">
          <p>{t("removeConfirm", { name: plugin.name })}</p>
          <div className="row">
            <button
              type="button"
              className="button button--danger"
              disabled={remove.isPending}
              onClick={() => {
                remove.mutate();
              }}
            >
              {remove.isPending ? t("removing") : t("removeYes")}
            </button>
            <button
              type="button"
              className="button"
              onClick={() => {
                setConfirming(false);
                remove.reset();
              }}
            >
              {t("removeNo")}
            </button>
          </div>
        </div>
      )}

      {failure !== undefined && (
        <p role="alert" className="notice notice--danger">
          {problemText(failure, t("changeFailed"))}
        </p>
      )}
    </li>
  );
}
