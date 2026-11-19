// SPDX-License-Identifier: MIT
import { describe, expect, it } from "vitest";

import { describeCalendar } from "./calendar-words";

describe("describeCalendar", () => {
  it.each([
    ["*-*-* 09:00:00", { kind: "daily", time: "09:00", day: undefined }],
    ["Mon *-*-* 03:00:00", { kind: "weekly", time: "03:00", day: "Mon" }],
    ["Mon..Fri *-*-* 08:30:00", { kind: "raw" }],
    ["hourly", { kind: "hourly" }],
    ["daily", { kind: "daily", time: "00:00", day: undefined }],
    ["weekly", { kind: "weekly", time: "00:00", day: "Mon" }],
    ["*-*-* 4,16:00:00", { kind: "raw" }],
    ["some other expression", { kind: "raw" }],
  ])("reads %s", (calendar, expected) => {
    expect(describeCalendar(calendar)).toMatchObject(expected);
  });
});
