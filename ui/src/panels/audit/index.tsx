// SPDX-License-Identifier: MIT
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import { useApi } from "../../app/api-context";
import { LoadError, Loading } from "../../shell/status";
import { ChainCheck } from "./chain-check";
import { KindIcon, kindOf, RecordRow, type RecordKind } from "./record-row";

type Show = "all" | RecordKind;

/** Audit: the record of what happened, with proof and testimony kept apart and the chain checked. */
export default function AuditPanel() {
  const { t } = useTranslation();
  const api = useApi();
  const client = useQueryClient();
  const [show, setShow] = useState<Show>("all");
  const records = useInfiniteQuery({
    queryKey: ["audit", "records"],
    queryFn: ({ pageParam }) => api.listAuditRecords(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    retry: false,
  });
  const all = records.data?.pages.flatMap((page) => page.items) ?? [];
  const items = show === "all" ? all : all.filter((r) => kindOf(r) === show);

  return (
    <div className="stack">
      <ChainCheck
        onCheckAgain={() => {
          void client.invalidateQueries({ queryKey: ["audit", "records"] });
        }}
      />

      <div role="group" aria-label={t("panels.audit.legend.label")} className="legend">
        <p>
          <KindIcon kind="proof" />
          <strong>{t("panels.audit.legend.proofTitle")}</strong> {t("panels.audit.legend.proof")}
        </p>
        <p>
          <KindIcon kind="testimony" />
          <strong>{t("panels.audit.legend.testimonyTitle")}</strong>{" "}
          {t("panels.audit.legend.testimony")}
        </p>
      </div>

      <div className="toggle" role="group" aria-label={t("panels.audit.showLabel")}>
        {(["all", "proof", "testimony"] as const).map((v) => (
          <button
            key={v}
            type="button"
            className="toggle__option"
            aria-pressed={show === v}
            onClick={() => {
              setShow(v);
            }}
          >
            {t(`panels.audit.show.${v}`)}
          </button>
        ))}
      </div>

      {records.isPending && <Loading label={t("panels.audit.loading")} />}
      {records.isError && (
        <LoadError error={records.error} onRetry={() => void records.refetch()} />
      )}
      {records.isSuccess && all.length === 0 && (
        <div className="empty">
          <h3>{t("panels.audit.empty.title")}</h3>
          <p>{t("panels.audit.empty.body")}</p>
        </div>
      )}
      {records.isSuccess && all.length > 0 && items.length === 0 && (
        <div className="empty">
          <h3>{t(`panels.audit.noneOfKind.${show === "all" ? "proof" : show}.title`)}</h3>
          <p>{t(`panels.audit.noneOfKind.${show === "all" ? "proof" : show}.body`)}</p>
        </div>
      )}
      {items.length > 0 && (
        <ul className="records" aria-label={t("panels.audit.listLabel")}>
          {items.map((record) => (
            <RecordRow key={record.seq} record={record} />
          ))}
        </ul>
      )}
      {records.hasNextPage && (
        <button
          type="button"
          className="button"
          disabled={records.isFetchingNextPage}
          onClick={() => void records.fetchNextPage()}
        >
          {records.isFetchingNextPage ? t("panels.audit.loadingMore") : t("panels.audit.more")}
        </button>
      )}
    </div>
  );
}
