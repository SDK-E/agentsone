// SPDX-License-Identifier: MIT
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

import { Brand } from "./brand";

export function NotFound() {
  const { t } = useTranslation();
  return (
    <main id="main" className="entry">
      <div className="entry__card stack">
        <Brand />
        <h1>{t("notFound.title")}</h1>
        <p>{t("notFound.body")}</p>
        <Link className="button button--primary" to="/">
          {t("setup.goHome")}
        </Link>
      </div>
    </main>
  );
}
