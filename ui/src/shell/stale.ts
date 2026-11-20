// SPDX-License-Identifier: MIT
import { createContext, useContext } from "react";

/** True while the last good answers are on screen and the machine cannot be reached. */
export const StaleContext = createContext(false);

/** Live labels use this to stop saying "Live" while nothing new can arrive. */
export function useStale(): boolean {
  return useContext(StaleContext);
}
