// SPDX-License-Identifier: MIT
import { percent } from "./format";

export type MeterTone = "ok" | "low" | "full";

interface MeterProps {
  /** What is measured, said to screen readers: "Disk /var". */
  label: string;
  value: number;
  max: number;
  /** The same reading in words for screen readers. */
  valueText: string;
  tone?: MeterTone;
}

/**
 * A bar that fills as `value` reaches `max`. The reading is always printed in words beside it
 * by the caller, so the bar and its colour are never the only way to learn the number.
 */
export function Meter({ label, value, max, valueText, tone = "ok" }: MeterProps) {
  return (
    <div
      className={`meter meter--${tone}`}
      role="meter"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={max}
      aria-valuenow={value}
      aria-valuetext={valueText}
    >
      <span className="meter__fill" style={{ inlineSize: `${String(percent(value, max))}%` }} />
    </div>
  );
}
