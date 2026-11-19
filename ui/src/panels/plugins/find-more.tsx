// SPDX-License-Identifier: MIT
import { useInfiniteQuery, useMutation, useQuery } from "@tanstack/react-query";
import { useState, type SyntheticEvent } from "react";
import { useTranslation } from "react-i18next";

import type { CatalogEntry, PluginKind, PluginSourceId } from "../../api/api";
import { useApi } from "../../app/api-context";
import { Loading } from "../../shell/status";
import { problemText } from "../../shell/problem-text";
import { KindChips } from "./installed-row";
import { PLUGIN_KINDS, PLUGIN_SOURCES } from "./labels";

/** One plugin in a source, with a button to install it. */
function CatalogRow({
  entry,
  onInstalled,
}: {
  entry: CatalogEntry;
  onInstalled: (name: string) => void;
}) {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.plugins" });
  const api = useApi();
  const install = useMutation({
    mutationFn: () => api.installPlugin({ source: entry.source, ref: entry.ref }),
    onSuccess: () => {
      onInstalled(entry.name);
    },
  });
  const homepage = entry.homepage;

  return (
    <li className="plugin">
      <div className="plugin__head">
        <h5 className="plugin__name">
          {entry.name}
          <span className="plugin__version"> {entry.version}</span>
        </h5>
        {entry.installed ? (
          <span className="plugin__state plugin__state--on">{t("find.installed")}</span>
        ) : (
          <button
            type="button"
            className="button button--primary"
            aria-label={t("find.installLabel", { name: entry.name })}
            disabled={install.isPending}
            onClick={() => {
              install.mutate();
            }}
          >
            {install.isPending ? t("find.installing") : t("find.install")}
          </button>
        )}
      </div>
      <p className="plugin__about">{entry.summary}</p>
      <KindChips kinds={entry.kinds} />
      <p className="plugin__meta">
        <span>{t(`source.${entry.source}`)}</span>
        <span>{t("find.by", { publisher: entry.publisher })}</span>
        {typeof homepage === "string" && /^https?:\/\//.test(homepage) && (
          <a href={homepage} target="_blank" rel="noopener noreferrer">
            {t("find.homepage")}
          </a>
        )}
      </p>
      <p className="plugin__ref">{entry.ref}</p>
      {install.isError && (
        <p role="alert" className="notice notice--danger">
          {problemText(install.error, t("find.failed"))}
        </p>
      )}
    </li>
  );
}

/** Search the plugin sources and install what is found. */
export function FindMore({ onInstalled }: { onInstalled: (name: string) => void }) {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.plugins" });
  const api = useApi();
  const [source, setSource] = useState<PluginSourceId | "">("");
  const [kind, setKind] = useState<PluginKind | "">("");
  const [draft, setDraft] = useState("");
  const [query, setQuery] = useState("");

  const sources = useQuery({
    queryKey: ["plugins", "sources"],
    queryFn: () => api.listPluginSources(),
    retry: false,
  });
  const offered: readonly PluginSourceId[] = sources.data
    ? PLUGIN_SOURCES.filter((id) => sources.data.some((s) => s.id === id && s.enabled))
    : PLUGIN_SOURCES;

  const results = useInfiniteQuery({
    queryKey: ["plugins", "catalog", source, kind, query],
    queryFn: ({ pageParam }) =>
      api.searchPluginCatalog({
        ...(query !== "" && { q: query }),
        ...(source !== "" && { source }),
        ...(kind !== "" && { kind }),
        ...(pageParam !== undefined && { cursor: pageParam }),
      }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    retry: false,
  });
  const entries = results.data?.pages.flatMap((page) => page.items) ?? [];

  const search = (event: SyntheticEvent) => {
    event.preventDefault();
    setQuery(draft.trim());
  };
  const clear = () => {
    setDraft("");
    setQuery("");
  };

  return (
    <section className="stack" aria-labelledby="plugins-find">
      <div className="stack-sm">
        <h4 id="plugins-find">{t("find.title")}</h4>
        <p className="hint">{t("find.lead")}</p>
      </div>

      <form role="search" className="plugins__search" onSubmit={search}>
        <label className="field">
          {t("find.searchLabel")}
          <input
            type="search"
            value={draft}
            placeholder={t("find.searchPlaceholder")}
            maxLength={200}
            onChange={(e) => {
              setDraft(e.target.value);
            }}
          />
        </label>
        <div className="plugins__filters">
          <label className="field">
            {t("find.sourceLabel")}
            <select
              value={source}
              onChange={(e) => {
                setSource(e.target.value as PluginSourceId | "");
              }}
            >
              <option value="">{t("find.allSources")}</option>
              {offered.map((id) => (
                <option key={id} value={id}>
                  {t(`source.${id}`)}
                </option>
              ))}
            </select>
          </label>
          <label className="field">
            {t("find.kindLabel")}
            <select
              value={kind}
              onChange={(e) => {
                setKind(e.target.value as PluginKind | "");
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
        <div className="row">
          <button type="submit" className="button button--primary">
            {t("find.searchSubmit")}
          </button>
          {(query !== "" || draft !== "") && (
            <button type="button" className="button" onClick={clear}>
              {t("find.searchClear")}
            </button>
          )}
        </div>
      </form>

      {results.isPending && <Loading label={t("find.loading")} />}
      {results.isError && (
        <div role="alert" className="notice notice--danger stack-sm">
          <p>{problemText(results.error, t("find.unreachable"))}</p>
          <p className="hint">{t("find.allowHint")}</p>
          <button type="button" className="button" onClick={() => void results.refetch()}>
            {t("find.unreachableRetry")}
          </button>
        </div>
      )}
      {results.isSuccess && entries.length === 0 && (
        <div className="empty">
          <h5>{t("find.empty.title")}</h5>
          <p>{t("find.empty.body")}</p>
        </div>
      )}
      {entries.length > 0 && (
        <ul className="plugins__list" aria-label={t("find.listLabel")}>
          {entries.map((entry) => (
            <CatalogRow
              key={`${entry.source}:${entry.ref}`}
              entry={entry}
              onInstalled={onInstalled}
            />
          ))}
        </ul>
      )}
      {results.hasNextPage && (
        <button
          type="button"
          className="button"
          disabled={results.isFetchingNextPage}
          onClick={() => void results.fetchNextPage()}
        >
          {results.isFetchingNextPage ? t("find.loadingMore") : t("find.showMore")}
        </button>
      )}
    </section>
  );
}
