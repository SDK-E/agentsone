// SPDX-License-Identifier: MIT
import { ApiError } from "../api/api";

/** The daemon's own plain-words explanation when it sent one, else the fallback. */
export function problemText(error: unknown, fallback: string): string {
  if (error instanceof ApiError && error.problem) {
    return error.problem.detail ?? error.problem.title;
  }
  return fallback;
}
