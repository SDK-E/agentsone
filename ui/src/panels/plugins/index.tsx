// SPDX-License-Identifier: MIT
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";

import type { PluginKind } from "../../api/api";
import { useApi } from "../../app/api-context";
import { LoadError, Loading } from "../../shell/status";
import { FindMore } from "./find-more";
import { InstalledRow } from "./installed-row";
import { PLUGIN_KINDS } from "./labels";

/** Plugins: what is installed, who installed it, and where to find more. */
export default function PluginsPanel() {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.plugins" });
  const api = useApi();
  const client = useQueryClient();
  const [kind, setKind] = useState<PluginKind | "">("");
  const [notice, setNotice] = useState<string | undefined>();

  const installed = useInfiniteQuery({
    queryKey: ["plugins", "installed", kind],
    queryFn: ({ pageParam }) =>
      api.listPlugins({
        ...(kind !== "" && { kind }),
        ...(pageParam !== undefined && { cursor: pageParam }),
      }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    retry: false,
  });
  const plugins = installed.data?.pages.flatMap((page) => page.items) ?? [];

  const changed = (message?: string) => {
    setNotice(message);
    void client.invalidateQueries({ queryKey: ["plugins"] });
  };

  return (
    <div className="plugins stack">
      <p className="hint">{t("lead")}</p>
      <p role="status" className="plugins__notice">
        {notice}
      </p>

      <section className="stack" aria-labelledby="plugins-installed">
        <div className="plugins__bar">
          <h4 id="plugins-installed">{t("installed.title")}</h4>
          <label className="field plugins__filter">
            {t("installed.filterLabel")}
            <select
              value={kind}
              onChange={(e) => {
                setKind(e.target.value as PluginKind | "");
                setNotice(undefined);
              }}
            >
              <option value="">{t("installed.allKinds")}</option>
              {PLUGIN_KINDS.map((id) => (
                <option key={id} value={id}>
                  {t(`kind.${id}`)}
                </option>
              ))}
            </select>
          </label>
        </div>

        {installed.isPending && <Loading label={t("installed.loading")} />}
        {installed.isError && (
          <LoadError error={installed.error} onRetry={() => void installed.refetch()} />
        )}
        {installed.isSuccess && plugins.length === 0 && (
          <div className="empty">
            <h5>{t("installed.empty.title")}</h5>
            <p>{t("installed.empty.body")}</p>
          </div>
        )}
        {plugins.length > 0 && (
          <ul className="plugins__list" aria-label={t("installed.listLabel")}>
            {plugins.map((plugin) => (
              <InstalledRow key={plugin.id} plugin={plugin} onChanged={changed} />
            ))}
          </ul>
        )}
        {installed.hasNextPage && (
          <button
            type="button"
            className="button"
            aria-label={t("installed.showMore")}
            disabled={installed.isFetchingNextPage}
            onClick={() => void installed.fetchNextPage()}
          >
            {installed.isFetchingNextPage ? t("installed.loadingMore") : t("installed.showMore")}
          </button>
        )}
      </section>

      <FindMore
        onInstalled={(name) => {
          changed(t("find.done", { name }));
        }}
      />
    </div>
  );
}
