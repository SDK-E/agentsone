// SPDX-License-Identifier: MIT
import { describe, expect, it } from "vitest";

import { duration, formatBytes, formatMoney, percent } from "./format";

describe("formatMoney", () => {
  it("shows micro-units as whole currency with two decimals", () => {
    expect(formatMoney(12_840_000, "USD")).toBe("$12.84");
    expect(formatMoney(0, "USD")).toBe("$0.00");
  });

  it("keeps small spends visible instead of rounding them to nothing", () => {
    expect(formatMoney(410_000, "USD")).toBe("$0.41");
    expect(formatMoney(3_000, "USD")).toBe("$0.003");
  });
});

describe("percent", () => {
  it("is the share of the whole, clamped to 0 through 100", () => {
    expect(percent(25, 100)).toBe(25);
    expect(percent(150, 100)).toBe(100);
    expect(percent(5, 0)).toBe(0);
    expect(percent(-5, 100)).toBe(0);
  });
});

describe("formatBytes", () => {
  it("uses binary units people see on their disks", () => {
    expect(formatBytes(3_221_225_472)).toBe("3.0 GiB");
    expect(formatBytes(1_048_576)).toBe("1.0 MiB");
    expect(formatBytes(900)).toBe("900 B");
  });
});

describe("duration", () => {
  it("says days, hours and minutes in short words", () => {
    expect(duration(17_100)).toEqual({ days: 0, hours: 4, minutes: 45 });
    expect(duration(90_000)).toEqual({ days: 1, hours: 1, minutes: 0 });
  });
});
