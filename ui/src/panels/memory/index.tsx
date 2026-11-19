// SPDX-License-Identifier: MIT
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type SyntheticEvent } from "react";
import { useTranslation } from "react-i18next";

import type { MemoryRecord, MemoryScope, Page } from "../../api/api";
import { useApi } from "../../app/api-context";
import { LoadError, Loading } from "../../shell/status";
import { TimeAgo } from "../shared/time-ago";
import { NoteDetail } from "./note-detail";
import { ScopeBadge } from "./scope-badge";

type ScopeChoice = MemoryScope | "all";
const SCOPES: readonly ScopeChoice[] = ["all", "private", "team", "common"];

interface Row {
  record: MemoryRecord;
  /** The matching text of a search hit, with matches wrapped in [ and ]. */
  snippet?: string;
}

/** Text with [matches] shown as marks. */
function Snippet({ text }: { text: string }) {
  const parts = text.split(/\[([^\]]+)\]/);
  return (
    <>
      {parts.map((part, index) =>
        index % 2 === 1 ? <mark key={`${String(index)}${part}`}>{part}</mark> : part,
      )}
    </>
  );
}

/** Memory: browse, search and read what the society remembers, and see who wrote each note. */
export default function MemoryPanel() {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.memory" });
  const api = useApi();
  const client = useQueryClient();
  const [scope, setScope] = useState<ScopeChoice>("all");
  const [draft, setDraft] = useState("");
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<string | undefined>();
  const [notice, setNotice] = useState<string | undefined>();

  const notes = useInfiniteQuery({
    queryKey: ["memory", scope, query],
    queryFn: async ({ pageParam }): Promise<Page<Row>> => {
      const filter = {
        ...(scope !== "all" && { scope }),
        ...(pageParam !== undefined && { cursor: pageParam }),
      };
      if (query === "") {
        const page = await api.listMemory(filter);
        return { items: page.items.map((record) => ({ record })), next_cursor: page.next_cursor };
      }
      const page = await api.searchMemory({ q: query, ...filter });
      return {
        items: page.items.map((hit) => ({ record: hit.record, snippet: hit.snippet })),
        next_cursor: page.next_cursor,
      };
    },
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    retry: false,
  });

  const rows = notes.data?.pages.flatMap((page) => page.items) ?? [];
  const open = rows.find((row) => row.record.id === selected)?.record;
  const searching = query !== "";

  const pick = (next: ScopeChoice) => {
    setScope(next);
    setSelected(undefined);
    setNotice(undefined);
  };
  const search = (event: SyntheticEvent) => {
    event.preventDefault();
    setQuery(draft.trim());
    setSelected(undefined);
    setNotice(undefined);
  };
  const clear = () => {
    setDraft("");
    setQuery("");
    setSelected(undefined);
  };

  return (
    <div className={`memory${open ? " memory--open" : ""}`}>
      <p className="hint memory__lead">{t("lead")}</p>

      <div className="memory__tools">
        <form role="search" className="memory__search" onSubmit={search}>
          <label className="field">
            {t("searchLabel")}
            <input
              type="search"
              value={draft}
              placeholder={t("searchPlaceholder")}
              maxLength={500}
              onChange={(e) => {
                setDraft(e.target.value);
              }}
            />
          </label>
          <div className="row">
            <button type="submit" className="button button--primary" disabled={draft.trim() === ""}>
              {t("searchSubmit")}
            </button>
            {(searching || draft !== "") && (
              <button type="button" className="button" onClick={clear}>
                {t("searchClear")}
              </button>
            )}
          </div>
        </form>
        <fieldset className="segmented">
          <legend>{t("scopeLabel")}</legend>
          <div className="segmented__options">
            {SCOPES.map((choice) => (
              <label key={choice} className="segmented__option">
                <input
                  type="radio"
                  name="memory-scope"
                  value={choice}
                  checked={scope === choice}
                  onChange={() => {
                    pick(choice);
                  }}
                />
                <span>{t(`scope.${choice}`)}</span>
              </label>
            ))}
          </div>
        </fieldset>
      </div>

      <p role="status" className="memory__notice">
        {notice}
      </p>

      <div className="memory__body">
        <div className="memory__list">
          {searching && notes.isSuccess && <p className="hint">{t("resultsFor", { q: query })}</p>}
          {notes.isPending && <Loading label={t("loading")} />}
          {notes.isError && <LoadError error={notes.error} onRetry={() => void notes.refetch()} />}
          {notes.isSuccess && rows.length === 0 && (
            <div className="empty">
              {searching ? (
                <>
                  <h4>{t("noResults.title")}</h4>
                  <p>{t("noResults.body", { q: query })}</p>
                </>
              ) : scope === "all" ? (
                <>
                  <h4>{t("empty.title")}</h4>
                  <p>{t("empty.body")}</p>
                </>
              ) : (
                <>
                  <h4>{t("emptyScope.title", { scope: t(`scope.${scope}`).toLowerCase() })}</h4>
                  <p>{t("emptyScope.body")}</p>
                </>
              )}
            </div>
          )}
          {rows.length > 0 && (
            <ul className="notes" aria-label={searching ? t("resultsLabel") : t("listLabel")}>
              {rows.map(({ record, snippet }) => (
                <li key={record.id}>
                  <button
                    type="button"
                    className="note"
                    aria-current={record.id === selected ? "true" : undefined}
                    onClick={() => {
                      setSelected(record.id);
                      setNotice(undefined);
                    }}
                  >
                    <span className="note__title">{record.title}</span>
                    {snippet !== undefined && (
                      <span className="note__snippet">
                        <Snippet text={snippet} />
                      </span>
                    )}
                    <span className="note__meta">
                      <ScopeBadge scope={record.scope} />
                      <span>{t("by", { name: record.owner })}</span>
                      <span>
                        {t("updated")} <TimeAgo iso={record.updated_at} />
                      </span>
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
          {notes.hasNextPage && (
            <button
              type="button"
              className="button"
              disabled={notes.isFetchingNextPage}
              onClick={() => void notes.fetchNextPage()}
            >
              {notes.isFetchingNextPage ? t("loadingMore") : t("showMore")}
            </button>
          )}
        </div>

        <div className="memory__detail">
          {open ? (
            <NoteDetail
              key={open.id}
              record={open}
              onBack={() => {
                setSelected(undefined);
              }}
              onDeleted={() => {
                setSelected(undefined);
                setNotice(t("delete.done"));
                void client.invalidateQueries({ queryKey: ["memory"] });
              }}
            />
          ) : (
            <p className="hint memory__pick">{t("pickOne")}</p>
          )}
        </div>
      </div>
    </div>
  );
}
