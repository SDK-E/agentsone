// SPDX-License-Identifier: MIT
import { useInfiniteQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

import type { Page } from "../../api/api";
import { LoadError, Loading } from "../../shell/status";

interface PagedListProps<T> {
  queryKey: readonly unknown[];
  fetchPage: (cursor?: string) => Promise<Page<T>>;
  /** Names the list for screen readers. */
  label: string;
  loading: string;
  empty: { title: string; body: string };
  itemKey: (item: T) => string;
  renderItem: (item: T) => ReactNode;
  /** Poll this often, in milliseconds, while the section is open. */
  refetchInterval?: number;
}

/** One machine list: waiting, failed, empty or loaded, with "Show more" for the next page. */
export function PagedList<T>({
  queryKey,
  fetchPage,
  label,
  loading,
  empty,
  itemKey,
  renderItem,
  refetchInterval,
}: PagedListProps<T>) {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.machine" });
  const query = useInfiniteQuery({
    queryKey,
    queryFn: ({ pageParam }) => fetchPage(pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    retry: false,
    ...(refetchInterval !== undefined && { refetchInterval }),
  });
  const items = query.data?.pages.flatMap((page) => page.items) ?? [];

  if (query.isPending) return <Loading label={loading} />;
  if (query.isError) return <LoadError error={query.error} onRetry={() => void query.refetch()} />;
  if (items.length === 0) {
    return (
      <div className="empty">
        <h4>{empty.title}</h4>
        <p>{empty.body}</p>
      </div>
    );
  }
  return (
    <>
      <ul className="machine__list" aria-label={label}>
        {items.map((item) => (
          <li key={itemKey(item)} className="machine__item">
            {renderItem(item)}
          </li>
        ))}
      </ul>
      {query.hasNextPage && (
        <button
          type="button"
          className="button"
          disabled={query.isFetchingNextPage}
          onClick={() => void query.fetchNextPage()}
        >
          {query.isFetchingNextPage ? t("loadingMore") : t("showMore")}
        </button>
      )}
    </>
  );
}
