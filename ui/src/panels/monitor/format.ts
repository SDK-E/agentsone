// SPDX-License-Identifier: MIT
//
// Small number formatters shared by the Monitor and the budget lines in other panels.

const MICROS = 1_000_000;

/** Money held as integer micro-units, shown as currency. Tiny amounts keep three decimals. */
export function formatMoney(micros: number, currency: string, locale = "en"): string {
  const value = micros / MICROS;
  const tiny = value !== 0 && Math.abs(value) < 0.01;
  return new Intl.NumberFormat(locale, {
    style: "currency",
    currency,
    minimumFractionDigits: tiny ? 3 : 2,
    maximumFractionDigits: tiny ? 3 : 2,
  }).format(value);
}

/** The share `part` is of `whole`, as a number from 0 to 100. */
export function percent(part: number, whole: number): number {
  if (whole <= 0) return 0;
  return Math.min(100, Math.max(0, (part / whole) * 100));
}

const UNITS = ["B", "KiB", "MiB", "GiB", "TiB"] as const;

/** Bytes in binary units: "3.0 GiB". */
export function formatBytes(bytes: number): string {
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < UNITS.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return unit === 0 ? `${String(bytes)} B` : `${value.toFixed(1)} ${UNITS[unit] ?? "B"}`;
}

/** Split seconds into whole days, hours and minutes. */
export function duration(seconds: number): { days: number; hours: number; minutes: number } {
  const minutes = Math.floor(seconds / 60);
  return {
    days: Math.floor(minutes / 1440),
    hours: Math.floor((minutes % 1440) / 60),
    minutes: minutes % 60,
  };
}
