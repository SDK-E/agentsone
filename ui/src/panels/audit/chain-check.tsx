// SPDX-License-Identifier: MIT
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { useApi } from "../../app/api-context";
import { LoadError, Loading } from "../../shell/status";

export const verifyKey = ["audit", "verify"] as const;

/** The chain check. Its newest record number tells the list where to start reading. */
export function useChainCheck() {
  const api = useApi();
  return useQuery({
    queryKey: verifyKey,
    queryFn: () => api.verifyAuditChain(),
    retry: false,
    staleTime: 30_000,
  });
}

const Tick = () => (
  <svg
    className="verdict__icon"
    viewBox="0 0 20 20"
    width="18"
    height="18"
    fill="none"
    stroke="currentColor"
    strokeWidth="2"
    strokeLinecap="round"
    strokeLinejoin="round"
    aria-hidden="true"
    focusable="false"
  >
    <circle cx="10" cy="10" r="8" />
    <path d="m6.5 10.2 2.5 2.5 4.5-5" />
  </svg>
);

const Cross = () => (
  <svg
    className="verdict__icon"
    viewBox="0 0 20 20"
    width="18"
    height="18"
    fill="none"
    stroke="currentColor"
    strokeWidth="2"
    strokeLinecap="round"
    strokeLinejoin="round"
    aria-hidden="true"
    focusable="false"
  >
    <circle cx="10" cy="10" r="8" />
    <path d="m7.5 7.5 5 5m0-5-5 5" />
  </svg>
);

/**
 * Re-checks the hash chain of the records written inside the machine, and says how much of it
 * the edge also holds. The edge's copy is the final proof; the check alone is only testimony.
 */
export function ChainCheck({
  check,
  onCheckAgain,
}: {
  check: ReturnType<typeof useChainCheck>;
  onCheckAgain: () => void;
}) {
  const { t } = useTranslation();
  const data = check.data;
  const waiting = data ? data.to_seq - data.host_acked_seq : 0;

  return (
    <section aria-labelledby="audit-check" className="card stack-sm">
      <div className="card__head">
        <h3 id="audit-check">{t("panels.audit.check.title")}</h3>
        <button
          type="button"
          className="button"
          disabled={check.isFetching}
          onClick={() => {
            void check.refetch();
            onCheckAgain();
          }}
        >
          {check.isFetching ? t("panels.audit.check.checking") : t("panels.audit.check.again")}
        </button>
      </div>
      {check.isPending && <Loading label={t("panels.audit.check.checking")} />}
      {check.isError && <LoadError error={check.error} onRetry={() => void check.refetch()} />}
      {data && (
        <div className="stack-sm">
          {data.ok ? (
            <p className="verdict verdict--ok">
              <Tick />
              {t("panels.audit.check.intact")}
            </p>
          ) : (
            <div role="alert" className="notice notice--danger stack-sm">
              <p className="verdict">
                <Cross />
                {t("panels.audit.check.broken", { seq: data.broken_at_seq ?? data.from_seq })}
              </p>
              <p>{t("panels.audit.check.brokenBody")}</p>
              <p>{t("panels.audit.check.brokenNext")}</p>
            </div>
          )}
          <p>{t("panels.audit.check.checked", { count: data.checked, to: data.to_seq })}</p>
          <p>
            {data.host_acked_seq === 0
              ? t("panels.audit.check.edgeNone")
              : t("panels.audit.check.edgeHolds", { seq: data.host_acked_seq })}
          </p>
          {waiting > 0 && data.host_acked_seq > 0 && (
            <p className="hint">{t("panels.audit.check.waiting", { count: waiting })}</p>
          )}
          <p className="hint">
            {t("panels.audit.check.fingerprint")}{" "}
            <code className="code">{data.head_hash.slice(0, 12)}</code>
          </p>
        </div>
      )}
      <p className="hint">{t("panels.audit.check.why")}</p>
    </section>
  );
}
