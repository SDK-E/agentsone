// SPDX-License-Identifier: MIT
import { useEffect, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

/**
 * The visiting human's name as a quiet button. It opens a small menu with sign out.
 * A disclosure, not an ARIA menu: one action does not need menu keyboard rules.
 */
export function AccountMenu({
  name,
  signingOut,
  onSignOut,
}: {
  name: string;
  signingOut: boolean;
  onSignOut: () => void;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const menuId = useId();
  const root = useRef<HTMLDivElement>(null);
  const toggle = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!open) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setOpen(false);
        toggle.current?.focus();
      }
    };
    const onPointer = (event: PointerEvent) => {
      if (!root.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("keydown", onKey);
    document.addEventListener("pointerdown", onPointer);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("pointerdown", onPointer);
    };
  }, [open]);

  return (
    <div className="account" ref={root}>
      <button
        ref={toggle}
        type="button"
        className="account__toggle"
        aria-expanded={open}
        aria-controls={menuId}
        aria-label={t("shell.account", { name })}
        onClick={() => {
          setOpen((o) => !o);
        }}
      >
        <span className="account__name">{name}</span>
        <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
          <path d="M4 6l4 4 4-4" fill="none" stroke="currentColor" strokeWidth="1.75" />
        </svg>
      </button>
      {open && (
        <div id={menuId} className="account__menu">
          <p className="hint">{t("shell.visitingAs", { name })}</p>
          <button type="button" className="button" disabled={signingOut} onClick={onSignOut}>
            {t("shell.signOut")}
          </button>
        </div>
      )}
    </div>
  );
}
