// SPDX-License-Identifier: MIT
import type { PluginKind, PluginSourceId } from "../../api/api";

export const PLUGIN_KINDS: readonly PluginKind[] = [
  "mcp-server",
  "skill",
  "agent-template",
  "runner",
  "panel",
  "theme",
  "emitter",
];

export const PLUGIN_SOURCES: readonly PluginSourceId[] = [
  "claude-code",
  "agentsone",
  "agent-skills",
  "mcp-registry",
];

export type ActorKind = "human" | "agent" | "system" | "host" | "plugin" | "other";

/** Splits an actor such as "agent:builder" into who and what kind of who. */
export function parseActor(actor: string): { kind: ActorKind; name: string } {
  const at = actor.indexOf(":");
  const head = at === -1 ? actor : actor.slice(0, at);
  const name = at === -1 ? actor : actor.slice(at + 1);
  if (
    head === "human" ||
    head === "agent" ||
    head === "system" ||
    head === "host" ||
    head === "plugin"
  ) {
    return { kind: head, name };
  }
  return { kind: "other", name: actor };
}
