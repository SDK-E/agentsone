// SPDX-License-Identifier: MIT
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import type { EngineLicence } from "../../api/api";
import { useApi } from "../../app/api-context";
import { setupKey } from "../../session/session";
import { problemText } from "../../shell/problem-text";
import { LoadError, Loading } from "../../shell/status";
import { Markdown } from "../memory/markdown";
import { TimeAgo } from "../shared/time-ago";

/** Anthropic's page for the terms that cover each install's own Claude account. */
export const ANTHROPIC_LEGAL_URL = "https://code.claude.com/docs/en/legal-and-compliance";

const megabytes = (bytes: number | null) =>
  bytes === null ? "?" : String(Math.round(bytes / 1e6));

/** How far the engine download has got, in words and a bar. */
function FetchStatus() {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.settings.engine.fetch" });
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
      <p>{t(`state.${engine.state}`)}</p>
      {running && (
        <>
          <progress
            className="progress"
            max={1}
            value={engine.progress}
            aria-label={t("progressLabel")}
          />
          <p className="hint">
            {t("bytes", {
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

/** The plain facts about Claude usage, said around the licence and nowhere else. */
function Facts() {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.settings.engine.facts" });
  return (
    <ul className="engine-facts">
      <li>{t("account")}</li>
      <li>{t("usage")}</li>
      <li>
        <a href={ANTHROPIC_LEGAL_URL} target="_blank" rel="noopener noreferrer">
          {t("link")}
          <span className="visually-hidden"> {t("newTab")}</span>
        </a>
      </li>
    </ul>
  );
}

function LicenceDisclosure({ licence }: { licence: EngineLicence }) {
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

/** The engine is on: who turned it on, the facts, the download and the licence they read. */
function EngineOn({ licence }: { licence: EngineLicence }) {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.settings.engine" });
  return (
    <>
      <p>
        <span>
          {licence.accepted_by === null
            ? t("acceptedUnknown")
            : t("accepted", { name: licence.accepted_by })}
        </span>
        {licence.accepted_at !== null && (
          <>
            {" · "}
            <TimeAgo iso={licence.accepted_at} />
          </>
        )}
      </p>
      <p className="hint">{t("version", { version: licence.version })}</p>
      <FetchStatus />
      <Facts />
      <LicenceDisclosure licence={licence} />
    </>
  );
}

/**
 * The engine is off. Nothing about it downloads until a human turns it on, reads the licence
 * with the facts beside it, and accepts.
 */
function EngineOff({ licence }: { licence: EngineLicence }) {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.settings.engine" });
  const api = useApi();
  const client = useQueryClient();
  const [open, setOpen] = useState(false);
  const opened = useRef(false);
  const turnOn = useRef<HTMLButtonElement>(null);
  const readFirst = useRef<HTMLParagraphElement>(null);
  const textId = useId();
  // Opening moves focus to the start of what to read; cancelling brings it back to the button.
  useEffect(() => {
    if (open) readFirst.current?.focus();
    else if (opened.current) turnOn.current?.focus();
  }, [open]);
  const accept = useMutation({
    mutationFn: (version: string) => api.acceptEngineLicence(version),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ["engine-licence"] });
      await client.invalidateQueries({ queryKey: ["engine-fetch"] });
      await client.invalidateQueries({ queryKey: setupKey });
    },
  });

  return (
    <>
      <p>{t("off")}</p>
      {!open && (
        <div className="row">
          <button
            ref={turnOn}
            type="button"
            className="button"
            onClick={() => {
              opened.current = true;
              setOpen(true);
            }}
          >
            {t("turnOn")}
          </button>
        </div>
      )}
      {open && (
        <div className="stack">
          <p ref={readFirst} tabIndex={-1}>
            {t("readFirst")}
          </p>
          <Facts />
          {/* Focusable so the licence can be scrolled from the keyboard. */}
          {/* eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex */}
          <section className="licence" aria-labelledby={textId} tabIndex={0}>
            <h5 id={textId} className="visually-hidden">
              {t("textLabel")}
            </h5>
            <Markdown text={licence.text} headingLevel={5} />
          </section>
          <p className="hint">{t("version", { version: licence.version })}</p>
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
                accept.mutate(licence.version);
              }}
            >
              {accept.isPending ? t("accepting") : t("accept")}
            </button>
            <button
              type="button"
              className="button"
              disabled={accept.isPending}
              onClick={() => {
                accept.reset();
                setOpen(false);
              }}
            >
              {t("cancel")}
            </button>
          </div>
        </div>
      )}
    </>
  );
}

/** The Claude engine: off by default; turned on here by reading and accepting its licence. */
export function EngineSection() {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.settings.engine" });
  const api = useApi();
  const titleId = useId();
  const licence = useQuery({
    queryKey: ["engine-licence"],
    queryFn: () => api.getEngineLicence(),
    retry: false,
  });

  return (
    <section aria-labelledby={titleId} className="stack">
      <h4 id={titleId}>{t("title")}</h4>
      {licence.isPending && <Loading label={t("loading")} />}
      {licence.isError && (
        <LoadError error={licence.error} onRetry={() => void licence.refetch()} />
      )}
      {licence.isSuccess &&
        (licence.data.accepted ? (
          <EngineOn licence={licence.data} />
        ) : (
          <EngineOff licence={licence.data} />
        ))}
    </section>
  );
}
