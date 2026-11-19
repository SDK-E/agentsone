// SPDX-License-Identifier: MIT
import { useTranslation } from "react-i18next";

import type { AuditRecord } from "../../api/api";

export type RecordKind = "proof" | "testimony";

/** Records written by the edge are proof. Records written inside the machine are testimony. */
export const kindOf = (record: AuditRecord): RecordKind =>
  record.origin === "host" ? "proof" : "testimony";

const when = (iso: string) =>
  new Intl.DateTimeFormat("en", { dateStyle: "medium", timeStyle: "medium" }).format(new Date(iso));

const show = (value: unknown): string =>
  typeof value === "string" ? value : JSON.stringify(value, null, 1);

/** A shield with a tick for proof; a speech bubble for testimony. Decoration: the kind is also written. */
export function KindIcon({ kind }: { kind: RecordKind }) {
  return (
    <svg
      className="kind__icon"
      viewBox="0 0 20 20"
      width="16"
      height="16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      {kind === "proof" ? (
        <>
          <path d="M10 2.5 16 5v4.5c0 3.6-2.5 6.3-6 8-3.5-1.7-6-4.4-6-8V5l6-2.5Z" />
          <path d="m7 10 2.2 2.2L13 8.2" />
        </>
      ) : (
        <>
          <path d="M3 4.5h14v8H9.5L6 16v-3.5H3v-8Z" />
          <path d="M6.5 8h7" />
        </>
      )}
    </svg>
  );
}

/** One audit record: its kind first, then who did what to what, and how it went. */
export function RecordRow({ record }: { record: AuditRecord }) {
  const { t } = useTranslation();
  const kind = kindOf(record);
  const [actorKind, actorName] = [record.actor.kind, record.actor.name];
  const details = Object.entries(record.details);

  return (
    <li className={`record record--${kind}`}>
      <p className="record__head">
        <span className={`kind kind--${kind}`}>
          <KindIcon kind={kind} />
          {t(`panels.audit.kind.${kind}`)}
        </span>
        <span className="hint">{t(`panels.audit.source.${kind}`)}</span>
        <span className="record__seq hint">{t("panels.audit.number", { seq: record.seq })}</span>
      </p>
      <p className="record__what">
        <code className="code record__action">{record.action}</code>
        <span className={`outcome-word outcome-word--${record.outcome}`}>
          {t(`panels.audit.outcome.${record.outcome}`)}
        </span>
      </p>
      <p className="record__meta">
        <span>{t(`panels.audit.actor.${actorKind}`)}</span>
        <span className="record__name">{actorName}</span>
        <span aria-hidden="true">→</span>
        <span className="record__name">
          {record.target.kind} {record.target.id}
        </span>
      </p>
      <p className="hint">
        <time dateTime={record.time}>{when(record.time)}</time>
      </p>
      <details className="record__details">
        <summary>{t("panels.audit.details")}</summary>
        {details.length > 0 && (
          <dl className="facts">
            {details.map(([key, value]) => (
              <div key={key}>
                <dt>{key}</dt>
                <dd>{show(value)}</dd>
              </div>
            ))}
          </dl>
        )}
        <p className="hint hash">
          {t("panels.audit.hash")} <code className="code">{record.hash}</code>
        </p>
        <p className="hint hash">
          {t("panels.audit.prevHash")} <code className="code">{record.prev_hash}</code>
        </p>
      </details>
    </li>
  );
}
