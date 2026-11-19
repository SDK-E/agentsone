// SPDX-License-Identifier: MIT
import { useTranslation } from "react-i18next";

/** Why an agent stopped, in the agent's words, under its name. Nothing is drawn without one. */
export function StateReason({ reason }: { reason: string | undefined }) {
  const { t } = useTranslation();
  if (!reason) return null;
  return (
    <p className="state-reason">
      <span className="state-reason__label">{t("agent.stateReason")}</span> {reason}
    </p>
  );
}
