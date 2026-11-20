// SPDX-License-Identifier: MIT
import { useCallback, useEffect, useRef, useState } from "react";

/**
 * Which ends of a sideways-scrolling row hide part of an item. An end hides something when its
 * first or last child is not wholly in view; the row's padding does not count. Spread `edges` on
 * the row (they become data-more-start and data-more-end for CSS) and attach `ref` and `onScroll`.
 */
export function useRowEdges<T extends HTMLElement>() {
  const ref = useRef<T>(null);
  const [more, setMore] = useState({ start: false, end: false });

  const measure = useCallback(() => {
    const row = ref.current;
    const first = row?.firstElementChild;
    const last = row?.lastElementChild;
    if (!row || !first || !last) return;
    const box = row.getBoundingClientRect();
    const start = first.getBoundingClientRect().left < box.left - 0.5;
    const end = last.getBoundingClientRect().right > box.right + 0.5;
    setMore((old) => (old.start === start && old.end === end ? old : { start, end }));
  }, []);

  useEffect(() => {
    measure();
    const row = ref.current;
    if (!row) return;
    const watcher = new ResizeObserver(measure);
    watcher.observe(row);
    for (const child of row.children) watcher.observe(child);
    return () => {
      watcher.disconnect();
    };
  }, [measure]);

  return {
    ref,
    onScroll: measure,
    edges: {
      "data-more-start": more.start || undefined,
      "data-more-end": more.end || undefined,
    },
  };
}
