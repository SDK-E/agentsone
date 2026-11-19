// SPDX-License-Identifier: MIT
import { Suspense, useEffect, useRef, useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";

import { panels, type PanelId } from "../panels/registry";
import { Loading } from "./status";
import { useMediaQuery } from "./use-media-query";

/** The panels under the home screen, as tabs. Arrow keys move between tabs, as WAI-ARIA asks. */
export function PanelTabs() {
  const { t } = useTranslation();
  const [active, setActive] = useState<PanelId>(panels[0]?.id ?? "monitor");
  const tabs = useRef<Partial<Record<PanelId, HTMLButtonElement | null>>>({});

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
      <div role="tablist" aria-label={t("panels.label")} className="tabs__list">
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
