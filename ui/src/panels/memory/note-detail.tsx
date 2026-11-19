// SPDX-License-Identifier: MIT
import { useMutation } from "@tanstack/react-query";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

import type { MemoryRecord } from "../../api/api";
import { useApi } from "../../app/api-context";
import { problemText } from "../../shell/problem-text";
import { TimeAgo } from "../shared/time-ago";
import { Markdown } from "./markdown";
import { ScopeBadge } from "./scope-badge";

/** One note in full: who wrote it, who can read it, where it came from and what it says. */
export function NoteDetail({
  record,
  onBack,
  onDeleted,
}: {
  record: MemoryRecord;
  onBack: () => void;
  onDeleted: () => void;
}) {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.memory" });
  const api = useApi();
  const titleId = useId();
  const [confirming, setConfirming] = useState(false);
  const remove = useMutation({
    mutationFn: () => api.deleteMemory(record.id),
    onSuccess: onDeleted,
  });

  const sourceRef = record.source.ref;
  return (
    <section aria-labelledby={titleId} className="note-detail stack">
      <button type="button" className="button button--quiet note-detail__back" onClick={onBack}>
        <span aria-hidden="true">←</span> {t("back")}
      </button>
      <header className="stack-sm">
        <h4 id={titleId} className="note-detail__title">
          {record.title}
        </h4>
        <p className="row">
          <ScopeBadge scope={record.scope} />
          <span className="hint">{t(`scopeAbout.${record.scope}`)}</span>
        </p>
      </header>

      <dl className="facts">
        <div>
          <dt>{t("detail.writer")}</dt>
          <dd>{record.owner}</dd>
        </div>
        <div>
          <dt>{t("detail.readers")}</dt>
          <dd>{t("detail.team", { scope: t(`scope.${record.scope}`), team: record.team })}</dd>
        </div>
        <div>
          <dt>{t("detail.source")}</dt>
          <dd>
            {t(`source.${record.source.kind}`)}
            {sourceRef !== undefined && <span className="facts__ref"> · {sourceRef}</span>}
          </dd>
        </div>
        <div>
          <dt>{t("detail.created")}</dt>
          <dd>
            <TimeAgo iso={record.created_at} />
          </dd>
        </div>
        <div>
          <dt>{t("detail.changed")}</dt>
          <dd>
            <TimeAgo iso={record.updated_at} /> ·{" "}
            <span>{t("detail.version", { count: record.revision })}</span>
          </dd>
        </div>
      </dl>

      {record.body.trim() === "" ? (
        <p className="hint">{t("detail.noText")}</p>
      ) : (
        <Markdown text={record.body} headingLevel={5} />
      )}

      {record.tags.length > 0 && (
        <div className="stack-sm">
          <h5>{t("detail.tags")}</h5>
          <ul className="chips">
            {record.tags.map((tag) => (
              <li key={tag} className="chip">
                {tag}
              </li>
            ))}
          </ul>
        </div>
      )}

      {record.links.length > 0 && (
        <div className="stack-sm">
          <h5>{t("detail.links")}</h5>
          <ul className="plain-list">
            {record.links.map((link) => (
              <li key={`${link.kind}:${link.ref}`}>
                {t(`link.${link.kind}`)}:{" "}
                {link.kind === "url" && /^https?:\/\//.test(link.ref) ? (
                  <a href={link.ref} target="_blank" rel="noopener noreferrer">
                    {link.title ?? link.ref}
                  </a>
                ) : (
                  <span>{link.title ?? link.ref}</span>
                )}
              </li>
            ))}
          </ul>
        </div>
      )}

      <div className="stack-sm note-detail__actions">
        <p className="hint">{t("changeHint")}</p>
        <div className="row">
          <Link className="button" to={`/agents/${encodeURIComponent(record.owner)}/chat`}>
            {t("change", { name: record.owner })}
          </Link>
          {!confirming && (
            <button
              type="button"
              className="button button--danger"
              onClick={() => {
                setConfirming(true);
              }}
            >
              {t("delete.start")}
            </button>
          )}
        </div>
        {confirming && (
          <div className="notice stack-sm">
            <p>{t("delete.confirm")}</p>
            <div className="row">
              <button
                type="button"
                className="button button--danger"
                disabled={remove.isPending}
                onClick={() => {
                  remove.mutate();
                }}
              >
                {remove.isPending ? t("delete.pending") : t("delete.yes")}
              </button>
              <button
                type="button"
                className="button"
                onClick={() => {
                  setConfirming(false);
                  remove.reset();
                }}
              >
                {t("delete.no")}
              </button>
            </div>
          </div>
        )}
        {remove.isError && (
          <p role="alert" className="notice notice--danger">
            {problemText(remove.error, t("delete.failed"))}
          </p>
        )}
      </div>
    </section>
  );
}
