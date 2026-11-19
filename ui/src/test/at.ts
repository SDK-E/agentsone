// SPDX-License-Identifier: MIT

/** The item at an index. Throws when there is none, so a test fails with a clear message. */
export function at<T>(items: readonly T[], index: number): T {
  const item = items[index];
  if (item === undefined) throw new Error(`Expected an item at position ${String(index)}`);
  return item;
}
