// SPDX-License-Identifier: MIT
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import { useApi } from "../../app/api-context";
import { LoadError, Loading } from "../../shell/status";

export const verifyKey = ["audit", "verify"] as const;

/**
 * Re-checks the hash chain of the records written inside the machine, and says how much of it
 * the edge also holds. The edge's copy is the final proof; the check alone is only testimony.
 */
export function ChainCheck({ onCheckAgain }: { onCheckAgain: () => void }) {
  const { t } = useTranslation();
  const api = useApi();
  const check = useQuery({
    queryKey: verifyKey,
    queryFn: () => api.verifyAuditChain(),
    retry: false,
    staleTime: 30_000,
  });
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
            <p className="verdict verdict--ok">{t("panels.audit.check.intact")}</p>
          ) : (
            <div role="alert" className="notice notice--danger stack-sm">
              <p className="verdict">
                {t("panels.audit.check.broken", { seq: data.broken_at_seq ?? data.from_seq })}
              </p>
              <p>{t("panels.audit.check.brokenBody")}</p>
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
