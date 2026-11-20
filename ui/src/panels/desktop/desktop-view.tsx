// SPDX-License-Identifier: MIT
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { desktopUrl, type VncConnector, type VncSession } from "./connection";

type State = "idle" | "connecting" | "connected" | "failed" | "ended";

// X11 keysyms and DOM key codes. The desktop binds Super+T to a terminal and Super+B to a web
// browser, so these buttons work on a phone too, where there is no right-click.
const SUPER_L = 0xffeb;
const KEY_T = 0x74;
const KEY_B = 0x62;

/** One human's desktop: a start card until the person opens it, then the screen and its tools. */
export function DesktopView({ human, connect }: { human: string; connect: VncConnector }) {
  const { t } = useTranslation();
  const screen = useRef<HTMLDivElement>(null);
  const session = useRef<VncSession | null>(null);
  const closing = useRef(false);
  const [state, setState] = useState<State>("idle");

  // Leaving the panel lets go of the connection; the desktop keeps running for a while.
  useEffect(
    () => () => {
      closing.current = true;
      session.current?.disconnect();
      session.current = null;
    },
    [],
  );

  const open = async () => {
    const target = screen.current;
    if (!target) return;
    closing.current = false;
    setState("connecting");
    try {
      session.current = await connect(target, desktopUrl(window.location, human), {
        onConnect: () => {
          setState("connected");
        },
        onDisconnect: (clean) => {
          session.current = null;
          if (closing.current) return;
          setState(clean ? "ended" : "failed");
        },
      });
    } catch {
      setState("failed");
    }
  };

  const close = () => {
    closing.current = true;
    session.current?.disconnect();
    session.current = null;
    setState("idle");
  };

  const launch = (keysym: number, code: string) => {
    const s = session.current;
    if (!s) return;
    s.sendKey(SUPER_L, "MetaLeft", true);
    s.sendKey(keysym, code);
    s.sendKey(SUPER_L, "MetaLeft", false);
    s.focus();
  };

  const fullScreen = () => {
    screen.current?.requestFullscreen().catch(() => {
      // Some browsers (iPhone Safari) refuse full screen for elements; the panel stays as it is.
    });
  };

  const showScreen = state === "connecting" || state === "connected";

  return (
    <div className="desktop">
      {state === "connected" && (
        <div className="desktop__tools" role="toolbar" aria-label={t("panels.desktop.tools")}>
          <div className="desktop__group">
            <button
              type="button"
              className="button"
              onClick={() => {
                launch(KEY_T, "KeyT");
              }}
            >
              {t("panels.desktop.terminal")}
            </button>
            <button
              type="button"
              className="button"
              onClick={() => {
                launch(KEY_B, "KeyB");
              }}
            >
              {t("panels.desktop.browser")}
            </button>
          </div>
          <div className="desktop__group">
            <button type="button" className="button" onClick={fullScreen}>
              {t("panels.desktop.fullScreen")}
            </button>
            <button type="button" className="button" onClick={close}>
              {t("panels.desktop.disconnect")}
            </button>
          </div>
        </div>
      )}
      {!showScreen && (
        <div className="empty desktop__start">
          <h3>{t("panels.desktop.title")}</h3>
          <p>{t("panels.desktop.intro")}</p>
          {state === "failed" && (
            <p role="alert" className="desktop__problem">
              {t("panels.desktop.failed")}
            </p>
          )}
          {state === "ended" && <p>{t("panels.desktop.ended")}</p>}
          <div>
            <button type="button" className="button button--primary" onClick={() => void open()}>
              {t(
                state === "failed"
                  ? "panels.desktop.retry"
                  : state === "ended"
                    ? "panels.desktop.reconnect"
                    : "panels.desktop.open",
              )}
            </button>
          </div>
        </div>
      )}
      {state === "connecting" && (
        <p role="status" className="hint">
          {t("panels.desktop.starting")}
        </p>
      )}
      <div
        ref={screen}
        className="desktop__screen"
        role="application"
        aria-label={t("panels.desktop.screen")}
        hidden={!showScreen}
      />
      {state === "connected" && <p className="hint">{t("panels.desktop.menuHint")}</p>}
    </div>
  );
}
