// SPDX-License-Identifier: MIT
/**
 * WCAG 2.2 contrast maths. See https://www.w3.org/TR/WCAG22/#dfn-contrast-ratio.
 * Colours are opaque #RRGGBB strings, the only colour form the theme schema allows.
 */

const HEX_COLOR = /^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i;

function linearChannel(byte: string): number {
  const value = Number.parseInt(byte, 16) / 255;
  return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
}

/** Relative luminance from 0 (black) to 1 (white). */
export function relativeLuminance(color: string): number {
  const match = HEX_COLOR.exec(color);
  if (!match) throw new Error(`expected an opaque #RRGGBB colour, got "${color}"`);
  const [, red = "", green = "", blue = ""] = match;
  return 0.2126 * linearChannel(red) + 0.7152 * linearChannel(green) + 0.0722 * linearChannel(blue);
}

/** Contrast ratio between two colours, from 1 to 21. Order does not matter. */
export function contrastRatio(first: string, second: string): number {
  const [lighter, darker] = [relativeLuminance(first), relativeLuminance(second)].sort(
    (a, b) => b - a,
  ) as [number, number];
  return (lighter + 0.05) / (darker + 0.05);
}
