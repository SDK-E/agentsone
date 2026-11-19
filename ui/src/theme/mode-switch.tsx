// SPDX-License-Identifier: MIT
import { useTranslation } from "react-i18next";

import type { ThemeMode } from "./theme";

const MODES: readonly ThemeMode[] = ["system", "light", "dark"];

/** Choose system, light or dark. Every theme carries both modes. */
export function ModeSwitch({
  mode,
  onChange,
}: {
  mode: ThemeMode;
  onChange: (mode: ThemeMode) => void;
}) {
  const { t } = useTranslation();
  return (
    <fieldset className="segmented">
      <legend>{t("theme.appearance")}</legend>
      <div className="segmented__options">
        {MODES.map((m) => (
          <label key={m} className="segmented__option">
            <input
              type="radio"
              name="appearance"
              value={m}
              checked={mode === m}
              onChange={() => {
                onChange(m);
              }}
            />
            <span>{t(`theme.mode.${m}`)}</span>
          </label>
        ))}
      </div>
      <p className="hint">{t("theme.hint")}</p>
    </fieldset>
  );
}
