// SPDX-License-Identifier: MIT
//
// The theme system: a Theme document (both modes) becomes CSS custom properties on the root
// element. Components only ever read the variables, never the JSON.
import type { ThemeSchema } from "@agentsone/contract";
import bundled from "@agentsone/theme-agents-one/theme.json";

export type Theme = ThemeSchema.Theme;
export type ThemeMode = "system" | "light" | "dark";
export type ResolvedMode = "light" | "dark";

/** The default Agents One Theme, bundled so the interface never waits on the network. */
export const defaultTheme = bundled as Theme;

const kebab = (name: string): string => name.replace(/[A-Z]/g, (c) => `-${c.toLowerCase()}`);

/** Pick light or dark. "system" follows the operating system's preference. */
export function resolveMode(mode: ThemeMode, prefersDark: boolean): ResolvedMode {
  if (mode === "system") return prefersDark ? "dark" : "light";
  return mode;
}

/** Every CSS custom property a theme defines for one mode, as name and value. */
export function themeVariables(theme: Theme, mode: ResolvedMode): Record<string, string> {
  const vars: Record<string, string> = {};
  const tokens = theme.modes[mode];
  // The token interfaces have no index signature, so read them as plain string records.
  const colors: Record<string, string> = { ...tokens.color };
  const shadows: Record<string, string> = { ...tokens.shadow };
  const radius: Record<string, string> = { ...theme.radius };
  const spacing: Record<string, string> = { ...theme.spacing };
  for (const [role, value] of Object.entries(colors)) {
    vars[`--color-${kebab(role)}`] = value;
  }
  for (const [size, value] of Object.entries(shadows)) {
    vars[`--shadow-${size}`] = value;
  }
  const { typography, motion } = theme;
  vars["--font-sans"] = typography.sans;
  vars["--font-mono"] = typography.mono;
  for (const [step, value] of Object.entries(typography.scale)) {
    vars[`--text-${step}`] = value.size;
    vars[`--leading-${step}`] = String(value.lineHeight);
  }
  for (const [weight, value] of Object.entries(typography.weights)) {
    vars[`--weight-${weight}`] = String(value);
  }
  for (const [size, value] of Object.entries(radius)) vars[`--radius-${size}`] = value;
  for (const [size, value] of Object.entries(spacing)) vars[`--space-${size}`] = value;
  vars["--motion-fast"] = `${String(motion.fastMs)}ms`;
  vars["--motion-normal"] = `${String(motion.normalMs)}ms`;
  vars["--motion-slow"] = `${String(motion.slowMs)}ms`;
  vars["--motion-easing"] = motion.easing;
  return vars;
}

/** Write a theme onto an element (the document root in the app) for one mode. */
export function applyTheme(root: HTMLElement, theme: Theme, mode: ResolvedMode): void {
  for (const [name, value] of Object.entries(themeVariables(theme, mode))) {
    root.style.setProperty(name, value);
  }
  root.style.setProperty("color-scheme", mode);
  root.dataset.mode = mode;
}
