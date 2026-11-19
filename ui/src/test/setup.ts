// SPDX-License-Identifier: MIT
import "@testing-library/jest-dom/vitest";

import { cleanup } from "@testing-library/react";
import { afterEach, vi } from "vitest";

// jsdom has no matchMedia. Tests start in light mode at desktop width unless they say otherwise.
export const media = { dark: false, phone: false };

Object.defineProperty(window, "matchMedia", {
  configurable: true,
  value: (query: string) => ({
    matches:
      (query.includes("prefers-color-scheme: dark") && media.dark) ||
      (query.includes("max-width") && media.phone),
    media: query,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  }),
});

// jsdom has no <dialog> methods.
HTMLDialogElement.prototype.showModal = function showModal(this: HTMLDialogElement) {
  this.open = true;
};
HTMLDialogElement.prototype.close = function close(this: HTMLDialogElement) {
  this.open = false;
  this.dispatchEvent(new Event("close"));
};

afterEach(() => {
  cleanup();
  media.dark = false;
  media.phone = false;
  window.localStorage.clear();
});
