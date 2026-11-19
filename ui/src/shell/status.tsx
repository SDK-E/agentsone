// SPDX-License-Identifier: MIT
import { useTranslation } from "react-i18next";

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

/** Something could not load: say what happened, then what to do. */
export function LoadError({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  const { t } = useTranslation();
  const said = t("status.loadFailed");
  const detail = problemText(error, said);
  return (
    <div role="alert" className="notice notice--danger stack-sm">
      <p>{said}</p>
      {detail !== said && <p className="notice__detail">{t("status.detail", { detail })}</p>}
      <button type="button" className="button" onClick={onRetry}>
        {t("status.retry")}
      </button>
    </div>
  );
}
