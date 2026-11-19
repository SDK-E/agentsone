// SPDX-License-Identifier: MIT
import { useTranslation } from "react-i18next";

/** The mark and name. The ring is the machine; the dot is a resident at work. */
export function Brand() {
  const { t } = useTranslation();
  return (
    <span className="brand">
      <svg className="brand__mark" viewBox="0 0 64 64" width="28" height="28" aria-hidden="true">
        <path
          className="brand__ring"
          d="M53.92 30.08A22 22 0 1 1 33.92 10.08"
          fill="none"
          strokeWidth="8"
          strokeLinecap="round"
        />
        <circle className="brand__dot" cx="47.56" cy="16.44" r="7" />
      </svg>
      <span className="brand__name">{t("app.name")}</span>
    </span>
  );
}
