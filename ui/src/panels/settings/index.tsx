// SPDX-License-Identifier: MIT
import { useTranslation } from "react-i18next";

import { AccountSection } from "./account";
import { EngineSection } from "./engine";
import { LookSection } from "./look";
import { PeopleSection } from "./people";

/** Settings: how the interface looks, your account, the people, and the Claude engine. */
export default function SettingsPanel() {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.settings" });
  return (
    <div className="settings stack">
      <LookSection />
      <AccountSection />
      <PeopleSection />
      <EngineSection />
      <p className="hint settings__control">{t("control")}</p>
    </div>
  );
}
