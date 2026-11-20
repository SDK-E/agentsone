// SPDX-License-Identifier: MIT
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import type { PtySession } from "../../api/api";
import { useApi } from "../../app/api-context";
import { problemText } from "../../shell/problem-text";
import { TimeAgo } from "../shared/time-ago";
import { PagedList } from "./paged-list";

const KEY = ["machine", "terminals"] as const;

/** Terminal sessions: open one as your own Linux user, see who has one, close yours. */
export function TerminalsSection() {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.machine.terminals" });
  const api = useApi();
  const client = useQueryClient();
  const [notice, setNotice] = useState<{ text: string; failed: boolean } | undefined>();

  const changed = (text: string) => {
    setNotice({ text, failed: false });
    void client.invalidateQueries({ queryKey: KEY });
  };
  const failed = (error: unknown, said: string) => {
    const detail = problemText(error, "");
    setNotice({ text: detail === "" ? said : `${said} ${detail}`, failed: true });
  };

  const create = useMutation({
    mutationFn: () => api.createPtySession(),
    onSuccess: (session) => {
      changed(t("opened", { user: session.user }));
    },
    onError: (error) => {
      failed(error, t("openFailed"));
    },
  });

  return (
    <div className="stack-sm">
      <p className="hint">{t("lead")}</p>
      <div className="row">
        <button
          type="button"
          className="button button--primary"
          disabled={create.isPending}
          onClick={() => {
            setNotice(undefined);
            create.mutate();
          }}
        >
          {create.isPending ? t("opening") : t("open")}
        </button>
      </div>
      <p className="hint">{t("typingNote")}</p>
      {notice?.failed === true ? (
        <p role="alert" className="notice notice--danger">
          {notice.text}
        </p>
      ) : (
        <p role="status" className="machine__notice">
          {notice?.text}
        </p>
      )}
      <PagedList
        queryKey={KEY}
        fetchPage={(cursor) => api.listPtySessions(cursor)}
        label={t("listLabel")}
        loading={t("loading")}
        empty={{ title: t("empty.title"), body: t("empty.body") }}
        itemKey={(s) => s.id}
        renderItem={(s) => (
          <Terminal
            session={s}
            onClosed={() => {
              changed(t("closed"));
            }}
            onFailed={(error) => {
              failed(error, t("closeFailed"));
            }}
          />
        )}
      />
    </div>
  );
}

function Terminal({
  session,
  onClosed,
  onFailed,
}: {
  session: PtySession;
  onClosed: () => void;
  onFailed: (error: unknown) => void;
}) {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.machine.terminals" });
  const api = useApi();
  const close = useMutation({
    mutationFn: () => api.closePtySession(session.id),
    onSuccess: onClosed,
    onError: onFailed,
  });
  const command = session.command.join(" ");
  return (
    <>
      <div className="machine__head">
        <p className="machine__title code">{command}</p>
        <span className={`status ${session.exited ? "status--paused" : "status--on"}`}>
          {session.exited
            ? session.exit_code === null
              ? t("exitedUnknown")
              : t("exited", { code: session.exit_code })
            : t("running")}
        </span>
      </div>
      <p className="machine__meta">
        <span>{t("user", { user: session.user })}</span>
        <span>{t("size", { cols: session.cols, rows: session.rows })}</span>
        <TimeAgo iso={session.created_at} />
      </p>
      {!session.exited && (
        <div className="row">
          <button
            type="button"
            className="button button--danger"
            aria-label={t("close", { command })}
            disabled={close.isPending}
            onClick={() => {
              close.mutate();
            }}
          >
            {close.isPending ? t("closing") : t("closeShort")}
          </button>
        </div>
      )}
    </>
  );
}
