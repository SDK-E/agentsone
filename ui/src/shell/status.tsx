// SPDX-License-Identifier: MIT
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

import { ApiError } from "../api/api";
import { problemText } from "./problem-text";

/** A calm waiting line, announced politely to screen readers. */
export function Loading({ label }: { label?: string }) {
  const { t } = useTranslation();
  return (
    <p role="status" className="loading">
      {label ?? t("status.loading")}
    </p>
  );
}

interface LoadErrorProps {
  error: unknown;
  onRetry: () => void;
  /** The headline when the caller may not see this (403), in the words of the place. */
  forbidden?: string;
  /** The headline when the request itself was wrong (400), in the words of the place. */
  invalid?: string;
  /** What to do instead of trying again, for a refusal or a wrong request. */
  action?: ReactNode;
}

/**
 * Something could not load: say why in a headline that matches the cause, then what to do.
 * Signed out: sign in again. Not allowed or a wrong request: trying again would not help, so
 * only the caller's own way out is offered. Anything else: the machine may still be starting.
 */
export function LoadError({ error, onRetry, forbidden, invalid, action }: LoadErrorProps) {
  const { t } = useTranslation();
  const status = error instanceof ApiError ? error.status : undefined;
  const said =
    status === 401
      ? t("status.signedOut")
      : status === 403
        ? (forbidden ?? t("status.forbidden"))
        : status === 400
          ? (invalid ?? t("status.invalid"))
          : t("status.loadFailed");
  const detail = problemText(error, said);
  const retry = (
    <button type="button" className="button" onClick={onRetry}>
      {t("status.retry")}
    </button>
  );
  return (
    <div role="alert" className="notice notice--danger stack-sm">
      <p>{said}</p>
      {detail !== said && <p className="notice__detail">{t("status.detail", { detail })}</p>}
      {status === 401 ? (
        <a className="button" href="/">
          {t("status.signInAgain")}
        </a>
      ) : status === 403 ? (
        action
      ) : status === 400 ? (
        (action ?? retry)
      ) : (
        retry
      )}
    </div>
  );
}
