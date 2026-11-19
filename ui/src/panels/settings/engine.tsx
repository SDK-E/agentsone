// SPDX-License-Identifier: MIT
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useId } from "react";
import { useTranslation } from "react-i18next";

import type { EngineLicence } from "../../api/api";
import { useApi } from "../../app/api-context";
import { setupKey } from "../../session/session";
import { problemText } from "../../shell/problem-text";
import { LoadError, Loading } from "../../shell/status";
import { Markdown } from "../memory/markdown";
import { TimeAgo } from "../shared/time-ago";

const megabytes = (bytes: number | null) =>
  bytes === null ? "?" : String(Math.round(bytes / 1e6));

/** How far the engine download has got, in words and a bar. */
function FetchStatus() {
  const { t } = useTranslation();
  const api = useApi();
  const status = useQuery({
    queryKey: ["engine-fetch"],
    queryFn: () => api.getEngineFetchStatus(),
    retry: false,
    refetchInterval: (q) =>
      q.state.data?.state === "ready" || q.state.data?.state === "failed" ? false : 2000,
  });

  if (status.isPending) return <Loading />;
  if (status.isError)
    return <LoadError error={status.error} onRetry={() => void status.refetch()} />;
  const engine = status.data;
  const running = engine.state !== "ready" && engine.state !== "failed";
  return (
    <div className="stack-sm">
      <p>{t(`setup.engine.state.${engine.state}`)}</p>
      {running && (
        <>
          <progress
            className="progress"
            max={1}
            value={engine.progress}
            aria-label={t("setup.engine.progressLabel")}
          />
          <p className="hint">
            {t("setup.engine.bytes", {
              done: megabytes(engine.bytes_done),
              total: megabytes(engine.bytes_total),
            })}
          </p>
        </>
      )}
      {engine.error !== null && (
        <p role="alert" className="notice notice--danger">
          {engine.error}
        </p>
      )}
    </div>
  );
}

function LicenceText({ licence }: { licence: EngineLicence }) {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.settings.engine" });
  return (
    <details className="disclosure">
      <summary>{t("readLicence")}</summary>
      <div className="disclosure__body">
        <Markdown text={licence.text} headingLevel={5} />
      </div>
    </details>
  );
}

/** The Claude engine: whether its licence was accepted, by whom, and how the download is going. */
export function EngineSection() {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.settings.engine" });
  const { t: tRoot } = useTranslation();
  const api = useApi();
  const client = useQueryClient();
  const titleId = useId();
  const licence = useQuery({
    queryKey: ["engine-licence"],
    queryFn: () => api.getEngineLicence(),
    retry: false,
  });
  const accept = useMutation({
    mutationFn: (version: string) => api.acceptEngineLicence(version),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ["engine-licence"] });
      await client.invalidateQueries({ queryKey: ["engine-fetch"] });
      await client.invalidateQueries({ queryKey: setupKey });
    },
  });

  return (
    <section aria-labelledby={titleId} className="stack">
      <h4 id={titleId}>{t("title")}</h4>
      {licence.isPending && <Loading label={t("loading")} />}
      {licence.isError && (
        <LoadError error={licence.error} onRetry={() => void licence.refetch()} />
      )}
      {licence.isSuccess && licence.data.accepted && (
        <>
          <p>
            <span>
              {licence.data.accepted_by === null
                ? t("acceptedUnknown")
                : t("accepted", { name: licence.data.accepted_by })}
            </span>
            {licence.data.accepted_at !== null && (
              <>
                {" · "}
                <TimeAgo iso={licence.data.accepted_at} />
              </>
            )}
          </p>
          <p className="hint">{t("version", { version: licence.data.version })}</p>
          <FetchStatus />
          <LicenceText licence={licence.data} />
        </>
      )}
      {licence.isSuccess && !licence.data.accepted && (
        <>
          <p>{t("notAccepted")}</p>
          <LicenceText licence={licence.data} />
          <p className="hint">{t("version", { version: licence.data.version })}</p>
          {accept.isError && (
            <p role="alert" className="notice notice--danger">
              {problemText(accept.error, t("acceptFailed"))}
            </p>
          )}
          <div className="row">
            <button
              type="button"
              className="button button--primary"
              disabled={accept.isPending}
              onClick={() => {
                accept.mutate(licence.data.version);
              }}
            >
              {tRoot("setup.licence.accept")}
            </button>
          </div>
        </>
      )}
    </section>
  );
}
