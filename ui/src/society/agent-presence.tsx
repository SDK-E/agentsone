// SPDX-License-Identifier: MIT
import type { LiveEntry } from "../api/api";

type State = LiveEntry["state"];

const ALIVE: readonly State[] = ["waking", "awake", "working"];

export function isAlive(state: State): boolean {
  return ALIVE.includes(state);
}

/**
 * The resident's initial in a ring. Ember and a slow breath when awake or working; a quiet open
 * ring when asleep. The state is also said in words next to it, never by colour alone.
 */
export function Presence({ name, state }: { name: string; state: State }) {
  return (
    <span className={`presence presence--${state}`} aria-hidden="true">
      {name.slice(0, 1).toUpperCase()}
    </span>
  );
}
