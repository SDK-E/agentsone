// SPDX-License-Identifier: MIT
//
// The panel registry. Each built-in panel lives in its own folder, ui/src/panels/<id>/, and
// exports a default component. Panels load lazily, so the home screen pays only for the open one.
// To build a panel, replace the placeholder inside its folder; the shell needs no change.
import { lazy, type ComponentType, type LazyExoticComponent } from "react";

export const PANEL_IDS = [
  "monitor",
  "tasks",
  "memory",
  "plugins",
  "ide",
  "desktop",
  "machine",
  "audit",
  "settings",
] as const;

export type PanelId = (typeof PANEL_IDS)[number];

export interface PanelDefinition {
  id: PanelId;
  /** i18n key of the tab label. */
  labelKey: `panels.${PanelId}.label`;
  component: LazyExoticComponent<ComponentType>;
}

const loaders: Record<PanelId, () => Promise<{ default: ComponentType }>> = {
  monitor: () => import("./monitor"),
  tasks: () => import("./tasks"),
  memory: () => import("./memory"),
  plugins: () => import("./plugins"),
  ide: () => import("./ide"),
  desktop: () => import("./desktop"),
  machine: () => import("./machine"),
  audit: () => import("./audit"),
  settings: () => import("./settings"),
};

export const panels: readonly PanelDefinition[] = PANEL_IDS.map((id) => ({
  id,
  labelKey: `panels.${id}.label`,
  component: lazy(loaders[id]),
}));
