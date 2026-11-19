// SPDX-License-Identifier: MIT
import assert from "node:assert/strict";
import { test } from "node:test";

import { contrastRatio, relativeLuminance } from "../src/contrast.ts";

await test("relative luminance is 0 for black and 1 for white", () => {
  assert.equal(relativeLuminance("#000000"), 0);
  assert.equal(relativeLuminance("#FFFFFF"), 1);
});

await test("relative luminance rejects colours that are not #RRGGBB", () => {
  assert.throws(() => relativeLuminance("#FFF"), /#RRGGBB/);
  assert.throws(() => relativeLuminance("teal"), /#RRGGBB/);
});

await test("contrast is 21 for black on white and 1 for a colour on itself", () => {
  assert.equal(contrastRatio("#000000", "#FFFFFF"), 21);
  assert.equal(contrastRatio("#137267", "#137267"), 1);
});

await test("contrast does not depend on the order of the colours", () => {
  assert.equal(contrastRatio("#137267", "#FFFFFF"), contrastRatio("#FFFFFF", "#137267"));
});

await test("contrast matches a known WCAG value", () => {
  // #767676 on white is the classic lightest grey that passes 4.5:1.
  assert.ok(Math.abs(contrastRatio("#767676", "#FFFFFF") - 4.54) < 0.01);
});
