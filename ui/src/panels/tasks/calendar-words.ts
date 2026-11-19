// SPDX-License-Identifier: MIT
//
// Schedules use systemd calendar expressions in UTC. Say the common shapes in plain words; for
// anything else the caller shows the expression as written.

export type CalendarWords =
  | { kind: "hourly" }
  | { kind: "daily"; time: string; day: undefined }
  | { kind: "weekly"; time: string; day: string }
  | { kind: "raw" };

const DAYS = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];
const TIME = /^(\d{2}):(\d{2}):(\d{2})$/;

export function describeCalendar(calendar: string): CalendarWords {
  const text = calendar.trim();
  if (text === "hourly") return { kind: "hourly" };
  if (text === "daily") return { kind: "daily", time: "00:00", day: undefined };
  if (text === "weekly") return { kind: "weekly", time: "00:00", day: "Mon" };

  const parts = text.split(/\s+/);
  const clock = parts.at(-1);
  const date = parts.at(-2);
  const match = clock ? TIME.exec(clock) : null;
  if (!match || date !== "*-*-*") return { kind: "raw" };
  const time = `${match[1] ?? ""}:${match[2] ?? ""}`;
  if (parts.length === 2) return { kind: "daily", time, day: undefined };
  const day = parts[0];
  if (parts.length === 3 && day !== undefined && DAYS.includes(day)) {
    return { kind: "weekly", time, day };
  }
  return { kind: "raw" };
}
