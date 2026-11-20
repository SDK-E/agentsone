// SPDX-License-Identifier: MIT
//
// The parts of noVNC (MPL-2.0, ships no types) the Desktop panel uses.

declare module "@novnc/novnc" {
  export interface RFBOptions {
    shared?: boolean;
    wsProtocols?: string[];
  }

  export default class RFB extends EventTarget {
    constructor(target: HTMLElement, urlOrChannel: string | WebSocket, options?: RFBOptions);
    scaleViewport: boolean;
    resizeSession: boolean;
    background: string;
    disconnect(): void;
    sendKey(keysym: number, code: string | null, down?: boolean): void;
    focus(options?: FocusOptions): void;
    addEventListener(type: "connect", listener: () => void): void;
    addEventListener(
      type: "disconnect",
      listener: (event: CustomEvent<{ clean: boolean }>) => void,
    ): void;
  }
}

// noVNC's logging module. The package exports only its RFB entry, so vite.config.ts maps this
// name to the module RFB itself imports.
declare module "@novnc/novnc/logging" {
  export function initLogging(level?: "debug" | "info" | "warn" | "error" | "none"): void;
}
