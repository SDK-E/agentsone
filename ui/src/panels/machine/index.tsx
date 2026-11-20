// SPDX-License-Identifier: MIT
import { useState, type ComponentType } from "react";
import { useTranslation } from "react-i18next";

import { MachineCard } from "../monitor/machine-card";
import { FilesSection } from "./files";
import { PackagesSection } from "./packages";
import { ProcessesSection } from "./processes";
import { ServicesSection } from "./services";
import { TerminalsSection } from "./terminals";
import { UsersSection } from "./users";

export const SECTIONS = [
  "usage",
  "processes",
  "services",
  "packages",
  "users",
  "files",
  "terminals",
] as const;
type Section = (typeof SECTIONS)[number];

const VIEWS: Record<Section, ComponentType> = {
  usage: MachineCard,
  processes: ProcessesSection,
  services: ServicesSection,
  packages: PackagesSection,
  users: UsersSection,
  files: FilesSection,
  terminals: TerminalsSection,
};

/** Machine: usage, processes, services, packages, users, files and terminals, one at a time. */
export default function MachinePanel() {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.machine" });
  const [section, setSection] = useState<Section>("usage");
  const View = VIEWS[section];

  return (
    <div className="machine stack">
      <p className="hint">{t("about")}</p>
      <fieldset className="segmented">
        <legend>{t("sectionsLabel")}</legend>
        <div className="segmented__options">
          {SECTIONS.map((choice) => (
            <label key={choice} className="segmented__option">
              <input
                type="radio"
                name="machine-section"
                value={choice}
                checked={section === choice}
                onChange={() => {
                  setSection(choice);
                }}
              />
              <span>{t(`section.${choice}`)}</span>
            </label>
          ))}
        </div>
      </fieldset>
      <section aria-labelledby="machine-section-title" className="stack-sm">
        <h3 id="machine-section-title" className="visually-hidden">
          {t(`section.${section}`)}
        </h3>
        <View />
      </section>
    </div>
  );
}
