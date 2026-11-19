// SPDX-License-Identifier: MIT
import { useTranslation } from "react-i18next";

import type { PanelId } from "./registry";

/** An honest stand-in until the panel is built: what it will show, nothing pretend. */
export function PanelPlaceholder({ id }: { id: PanelId }) {
  const { t } = useTranslation();
  return (
    <div className="placeholder">
      <h3>{t(`panels.${id}.label`)}</h3>
      <p>{t(`panels.${id}.about`)}</p>
      <p className="hint">{t("panels.coming")}</p>
    </div>
  );
}
