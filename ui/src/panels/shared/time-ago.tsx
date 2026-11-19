// SPDX-License-Identifier: MIT
import { relativeTime, useNow } from "../../society/relative-time";

/** "2 hours ago" for people; the exact date and time on hover and in the markup. */
export function TimeAgo({ iso }: { iso: string }) {
  const now = useNow();
  return (
    <time dateTime={iso} title={new Date(iso).toLocaleString()}>
      {relativeTime(iso, now)}
    </time>
  );
}
