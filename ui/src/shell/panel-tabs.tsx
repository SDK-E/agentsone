// SPDX-License-Identifier: MIT
import {
  Suspense,
  useCallback,
  useEffect,
  useRef,
  useState,
  type KeyboardEvent,
  type Ref,
} from "react";
import { useTranslation } from "react-i18next";

import { panels, type PanelId } from "../panels/registry";
import { usePanelRequest } from "./panel-request";
import { Loading } from "./status";
import { useMediaQuery } from "./use-media-query";

/** The least solid width of a cue: the arrow (4px in, 18px wide) and a little room. Keep in step with global.css. */
const CUE_SOLID_MIN = 26;
/** The least width of a cue, fade included: CUE_SOLID_MIN and the 8px fade of global.css. */
const CUE_WIDTH_MIN = 34;
/** How far before the end of a cut tab its cue stops being solid: the fade (8px) and 2px, so the cue ends before the next tab's focus ring begins. */
const CUE_TAB_PADDING = 10;
/** What a chosen tab keeps clear of at each edge: the whole cue, a focus ring and 1px. Matches the scroll padding in global.css. */
const CUE_CLEARANCE = 54;

/** A fade and an arrow at one edge of the tab row, shown by CSS when tabs are hidden there. */
function Chevron({ side, ref }: { side: "start" | "end"; ref: Ref<HTMLSpanElement> }) {
  return (
    <span ref={ref} className={`tabs__cue tabs__cue--${side}`} aria-hidden="true">
      <svg viewBox="0 0 20 20" width="18" height="18" fill="none" stroke="currentColor">
        <path
          d={side === "end" ? "m7.5 4.5 5.5 5.5-5.5 5.5" : "m12.5 4.5-5.5 5.5 5.5 5.5"}
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
    </span>
  );
}

/** The panels under the home screen, as tabs. Arrow keys move between tabs, as WAI-ARIA asks. */
export function PanelTabs() {
  const { t } = useTranslation();
  const { request } = usePanelRequest();
  const [active, setActive] = useState<PanelId>(request?.panel ?? panels[0]?.id ?? "monitor");
  // Something on the home screen asked for a panel: turn to it, once per request.
  const [turned, setTurned] = useState(request?.seq);
  if (request && request.seq !== turned) {
    setTurned(request.seq);
    setActive(request.panel);
  }
  const tabs = useRef<Partial<Record<PanelId, HTMLButtonElement | null>>>({});
  const list = useRef<HTMLDivElement>(null);
  const cues = useRef<Record<"start" | "end", HTMLSpanElement | null>>({ start: null, end: null });
  const [more, setMore] = useState({ start: false, end: false });

  // On a phone the tabs do not all fit. Say where more of them are hiding. A side has more when
  // the first or last tab is not wholly in view; the row's own padding does not count, so a row
  // scrolled until its last tab is flush with the edge has nothing more to the end.
  //
  // A tab cut by the edge must never show a part of its label. So each cue is made as wide as the
  // cut tab needs: solid over the label, then a short fade over the tab's own empty padding.
  const measure = useCallback(() => {
    const el = list.current;
    const first = tabs.current[panels[0]?.id ?? "monitor"];
    const last = tabs.current[panels[panels.length - 1]?.id ?? "monitor"];
    if (!el || !first || !last) return;
    const row = el.getBoundingClientRect();
    const start = first.getBoundingClientRect().left < row.left - 0.5;
    const end = last.getBoundingClientRect().right > row.right + 0.5;
    setMore((old) => (old.start === start && old.end === end ? old : { start, end }));

    // A tab is out of the cue's way only when it lies wholly beyond the least a cue can be. One
    // that is cut, or that sits in that least space, goes under the cue whole.
    let startEdge = 0;
    let endEdge = 0;
    for (const tab of Object.values(tabs.current)) {
      if (!tab) continue;
      const box = tab.getBoundingClientRect();
      if (start && box.right > row.left && box.left < row.left + CUE_WIDTH_MIN) {
        startEdge = Math.max(startEdge, box.right - row.left);
      }
      if (end && box.left < row.right && box.right > row.right - CUE_WIDTH_MIN) {
        endEdge = Math.max(endEdge, row.right - box.left);
      }
    }
    // The solid part ends where the cut tab's label ends (its padding and the fade lie beyond).
    // It never gets shorter than what the arrow needs.
    for (const side of ["start", "end"] as const) {
      const edge = side === "start" ? startEdge : endEdge;
      cues.current[side]?.style.setProperty(
        "--cue-solid",
        `${String(Math.max(CUE_SOLID_MIN, Math.ceil(edge) - CUE_TAB_PADDING))}px`,
      );
    }
  }, []);

  // The row can change without scrolling: a narrower window, or the chosen tab turning bold and
  // so wider. Measure again whenever the row or any tab changes size.
  useEffect(() => {
    measure();
    const el = list.current;
    if (!el) return;
    const watcher = new ResizeObserver(measure);
    watcher.observe(el);
    for (const tab of Object.values(tabs.current)) if (tab) watcher.observe(tab);
    return () => {
      watcher.disconnect();
    };
  }, [measure]);

  // The chosen tab, even when picked by the keyboard, must not sit out of sight or under a cue.
  // When it is not wholly in the clear, bring it to the start of the row: that is a place the
  // row's scroll snap rests at, so the snap and this scroll agree and nothing pulls it back.
  useEffect(() => {
    const tab = tabs.current[active];
    const el = list.current;
    if (!tab || !el) return;
    const row = el.getBoundingClientRect();
    const box = tab.getBoundingClientRect();
    const clear = parseFloat(getComputedStyle(el).scrollPaddingInlineStart) || CUE_CLEARANCE;
    const whole = box.left >= row.left + clear - 0.5 && box.right <= row.right - clear + 0.5;
    if (!whole) tab.scrollIntoView({ inline: "start", block: "nearest" });
  }, [active]);

  const move = (event: KeyboardEvent, index: number) => {
    const last = panels.length - 1;
    const next =
      event.key === "ArrowRight"
        ? index === last
          ? 0
          : index + 1
        : event.key === "ArrowLeft"
          ? index === 0
            ? last
            : index - 1
          : event.key === "Home"
            ? 0
            : event.key === "End"
              ? last
              : undefined;
    const panel = next === undefined ? undefined : panels[next];
    if (!panel) return;
    event.preventDefault();
    setActive(panel.id);
    tabs.current[panel.id]?.focus();
  };

  const current = panels.find((p) => p.id === active);
  const Panel = current?.component;
  return (
    <div className="tabs">
      <div
        className="tabs__scroller"
        data-more-start={more.start || undefined}
        data-more-end={more.end || undefined}
      >
        <div
          ref={list}
          role="tablist"
          aria-label={t("panels.label")}
          className="tabs__list"
          onScroll={measure}
        >
          {panels.map((p, i) => (
            <button
              key={p.id}
              ref={(el) => {
                tabs.current[p.id] = el;
              }}
              type="button"
              role="tab"
              id={`tab-${p.id}`}
              aria-selected={p.id === active}
              aria-controls={`tabpanel-${p.id}`}
              tabIndex={p.id === active ? 0 : -1}
              className="tabs__tab"
              onClick={() => {
                setActive(p.id);
              }}
              onKeyDown={(e) => {
                move(e, i);
              }}
            >
              {t(p.labelKey)}
            </button>
          ))}
        </div>
        <Chevron
          side="start"
          ref={(el) => {
            cues.current.start = el;
          }}
        />
        <Chevron
          side="end"
          ref={(el) => {
            cues.current.end = el;
          }}
        />
      </div>
      <div
        role="tabpanel"
        id={`tabpanel-${active}`}
        aria-labelledby={`tab-${active}`}
        tabIndex={0}
        className="tabs__panel"
      >
        {Panel && (
          <Suspense fallback={<Loading />}>
            <Panel />
          </Suspense>
        )}
      </div>
    </div>
  );
}

/** Panels under the society on wide screens; in a sheet on a phone. */
export function PanelsArea() {
  const { t } = useTranslation();
  const phone = useMediaQuery("(max-width: 767px)");
  const sheet = useRef<HTMLDialogElement>(null);
  const [open, setOpen] = useState(false);
  const { request } = usePanelRequest();
  // On a phone the panels live in a sheet: a request for a panel opens it, once per request.
  const [opened, setOpened] = useState(request?.seq);
  if (request && request.seq !== opened) {
    setOpened(request.seq);
    if (phone) setOpen(true);
  }

  useEffect(() => {
    const dialog = sheet.current;
    if (!dialog) return;
    if (open && !dialog.open) dialog.showModal();
    if (!open && dialog.open) dialog.close();
  }, [open]);

  if (!phone) {
    return (
      <section aria-labelledby="panels-title" className="panel panels">
        <h2 id="panels-title" className="visually-hidden">
          {t("panels.label")}
        </h2>
        <PanelTabs />
      </section>
    );
  }
  return (
    <section aria-labelledby="panels-title" className="panels panels--phone">
      <h2 id="panels-title" className="visually-hidden">
        {t("panels.label")}
      </h2>
      <button
        type="button"
        className="button sheet-open"
        aria-haspopup="dialog"
        onClick={() => {
          setOpen(true);
        }}
      >
        {t("panels.open")}
      </button>
      <dialog
        ref={sheet}
        className="sheet"
        aria-labelledby="sheet-title"
        onClose={() => {
          setOpen(false);
        }}
      >
        <div className="sheet__header">
          <h2 id="sheet-title">{t("panels.label")}</h2>
          <button
            type="button"
            className="button"
            onClick={() => {
              setOpen(false);
            }}
          >
            {t("panels.close")}
          </button>
        </div>
        {open && <PanelTabs />}
      </dialog>
    </section>
  );
}
