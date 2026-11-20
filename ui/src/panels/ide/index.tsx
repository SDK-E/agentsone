// SPDX-License-Identifier: MIT
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";

import type { IdeServer } from "../../api/api";
import { useApi } from "../../app/api-context";
import { useMe } from "../../session/session";
import { problemText } from "../../shell/problem-text";
import { LoadError, Loading } from "../../shell/status";

/** While the editor starts, ask how it is doing this often. */
const STARTING_POLL_MS = 1000;

/** IDE: the open-source VS Code server of the signed-in person, opened in place. */
export default function IdePanel() {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.ide" });
  const me = useMe();

  if (me.isPending) return <Loading />;
  if (me.isError) return <LoadError error={me.error} onRetry={() => void me.refetch()} />;
  if (me.data.kind !== "human") {
    return (
      <div className="empty">
        <h4>{t("peopleOnly.title")}</h4>
        <p>{t("peopleOnly.body")}</p>
      </div>
    );
  }
  return <Editor human={me.data.human.name} />;
}

function Editor({ human }: { human: string }) {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.ide" });
  const api = useApi();
  const client = useQueryClient();
  const key = ["ide", "server", human] as const;

  const status = useQuery({ queryKey: ["ide", "status"], queryFn: () => api.getIdeStatus() });
  const server = useQuery({
    queryKey: key,
    queryFn: () => api.getIdeServer(human),
    retry: false,
    refetchInterval: (query) => (query.state.data?.state === "starting" ? STARTING_POLL_MS : false),
  });
  const start = useMutation({
    mutationFn: () => api.startIdeServer(human),
    onSuccess: (next) => {
      client.setQueryData<IdeServer>(key, next);
    },
  });
  const stop = useMutation({
    mutationFn: () => api.stopIdeServer(human),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: key });
    },
  });
  const failure = start.error ?? stop.error;
  const startEditor = () => {
    stop.reset();
    start.mutate();
  };

  return (
    <div className="ide stack-sm">
      {status.data && (
        <p className="hint">
          {t("build", { build: status.data.build, version: status.data.version })}
        </p>
      )}
      {failure !== null && (
        <div role="alert" className="notice notice--danger stack-sm">
          <p>{start.error ? t("startFailed") : t("stopFailed")}</p>
          {problemText(failure, "") !== "" && (
            <p className="notice__detail">{problemText(failure, "")}</p>
          )}
        </div>
      )}

      {server.isPending && <Loading label={t("looking")} />}
      {server.isError && <LoadError error={server.error} onRetry={() => void server.refetch()} />}

      {server.data?.state === "stopped" && (
        <div className="empty">
          <h4>{t("stopped.title")}</h4>
          <p>{t("stopped.body")}</p>
          <div className="row">
            <StartButton pending={start.isPending} onStart={startEditor} />
          </div>
        </div>
      )}

      {server.data?.state === "starting" && (
        <p role="status" className="ide__starting">
          {t("starting")}
        </p>
      )}

      {server.data?.state === "failed" && (
        <div role="alert" className="notice notice--danger stack-sm">
          <p>{t("failed")}</p>
          {server.data.error !== null && (
            <p className="notice__detail">{t("said", { detail: server.data.error })}</p>
          )}
          <div className="row">
            <StartButton pending={start.isPending} onStart={startEditor} again />
          </div>
        </div>
      )}

      {server.data?.state === "running" && server.data.url !== null && (
        <>
          <div className="row ide__bar">
            <a className="button" href={server.data.url} target="_blank" rel="noopener noreferrer">
              {t("newTab")}
            </a>
            <button
              type="button"
              className="button"
              disabled={stop.isPending}
              onClick={() => {
                start.reset();
                stop.mutate();
              }}
            >
              {stop.isPending ? t("stopping") : t("stop")}
            </button>
          </div>
          <iframe className="ide__frame" src={server.data.url} title={t("frameTitle", { human })} />
        </>
      )}
    </div>
  );
}

function StartButton({
  pending,
  onStart,
  again = false,
}: {
  pending: boolean;
  onStart: () => void;
  again?: boolean;
}) {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.ide" });
  return (
    <button type="button" className="button button--primary" disabled={pending} onClick={onStart}>
      {pending ? t("startPending") : again ? t("startAgain") : t("start")}
    </button>
  );
}
