// SPDX-License-Identifier: MIT
import { useTranslation } from "react-i18next";

import { ModeSwitch } from "../../theme/mode-switch";
import { useThemeSelection } from "../../theme/theme-provider";

/** Settings. Appearance works today; the rest of the page arrives with the panels work. */
export default function SettingsPanel() {
  const { t } = useTranslation();
  const { mode, choose } = useThemeSelection();
  return (
    <div className="stack">
      <ModeSwitch mode={mode} onChange={choose} />
      <div className="placeholder">
        <p>{t("panels.settings.about")}</p>
        <p className="hint">{t("panels.coming")}</p>
      </div>
    </div>
  );
}
