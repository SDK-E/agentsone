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

// jsdom has no canvas. It returns null anyway, but logs "Not implemented" first whenever axe
// measures text with one, which would leave noise in a clean test run.
HTMLCanvasElement.prototype.getContext = function getContext() {
  return null;
} as typeof HTMLCanvasElement.prototype.getContext;

// jsdom has no layout: no scrolling into view and no resize observer.
Element.prototype.scrollIntoView = function scrollIntoView() {
  // Nothing to scroll without layout. Tests spy on it to see what was brought into view.
};
class NoLayoutResizeObserver {
  observe() {
    // Nothing to watch without layout.
  }
  unobserve() {
    // Nothing to watch without layout.
  }
  disconnect() {
    // Nothing to watch without layout.
  }
}
globalThis.ResizeObserver = NoLayoutResizeObserver;

afterEach(() => {
  cleanup();
  media.dark = false;
  media.phone = false;
  window.localStorage.clear();
});
