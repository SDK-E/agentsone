// SPDX-License-Identifier: MIT
//
// The VNC client behind the Desktop panel. The app uses noVNC; tests pass a fake connector.

/** What the panel asks of a VNC client. */
export interface VncSession {
  disconnect(): void;
  /** Press (down true), release (down false) or tap (down left out) one key. */
  sendKey(keysym: number, code: string, down?: boolean): void;
  focus(): void;
}

/** What the VNC client tells the panel. */
export interface VncEvents {
  onConnect(): void;
  /** clean is false when the connection failed rather than ended. */
  onDisconnect(clean: boolean): void;
}

/** Starts a VNC client drawing into target. */
export type VncConnector = (
  target: HTMLElement,
  url: string,
  events: VncEvents,
) => Promise<VncSession>;

/** The WebSocket subprotocol the daemon's desktop bridge asks for. */
export const SUBPROTOCOL = "agentsone.vnc.v1";

/** The desktop bridge of human, on the origin the interface is served from. */
export function desktopUrl(location: Pick<Location, "protocol" | "host">, human: string): string {
  const scheme = location.protocol === "https:" ? "wss:" : "ws:";
  return `${scheme}//${location.host}/desktop/${encodeURIComponent(human)}/vnc`;
}

/**
 * noVNC, loaded only when a desktop opens. Its own console log is off: the panel says what went
 * wrong in words a person can act on.
 */
export const connectNoVnc: VncConnector = async (target, url, events) => {
  const [{ default: RFB }, { initLogging }] = await Promise.all([
    import("@novnc/novnc"),
    import("@novnc/novnc/logging"),
  ]);
  initLogging("none");
  const rfb = new RFB(target, url, { wsProtocols: [SUBPROTOCOL], shared: true });
  rfb.scaleViewport = true;
  rfb.resizeSession = false;
  rfb.background = "var(--color-surface-sunken)";
  rfb.addEventListener("connect", () => {
    events.onConnect();
  });
  rfb.addEventListener("disconnect", (e) => {
    events.onDisconnect(e.detail.clean);
  });
  return rfb;
};
