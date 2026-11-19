// SPDX-License-Identifier: MIT
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, Navigate, useNavigate } from "react-router";

import { isSignedOut, useMe, useSetupStatus, useSignOut } from "../session/session";
import { Brand } from "./brand";
import { LoadError, Loading } from "./status";

/**
 * The frame around every signed-in screen: skip link, the brand, who is visiting and sign out.
 * Sends people to setup on first boot and to sign in when no session is open.
 */
export function AppShell({ children }: { children: ReactNode }) {
  const { t } = useTranslation();
  const setup = useSetupStatus();
  const me = useMe();
  const signOut = useSignOut();
  const navigate = useNavigate();

  if (setup.data?.phase === "needs-human") return <Navigate to="/setup" replace />;
  if (me.isError && isSignedOut(me.error)) return <Navigate to="/sign-in" replace />;
  if (setup.isPending || me.isPending) {
    return (
      <main id="main" className="entry">
        <Loading label={t("status.waking")} />
      </main>
    );
  }
  if (setup.isError || me.isError) {
    const failed = setup.isError ? setup : me;
    return (
      <main id="main" className="entry">
        <div className="entry__card">
          <Brand />
          <h1>{t("status.unreachableTitle")}</h1>
          <LoadError error={failed.error} onRetry={() => void failed.refetch()} />
        </div>
      </main>
    );
  }

  const human = me.data.kind === "human" ? me.data.human : undefined;
  return (
    <>
      <a href="#main" className="skip-link">
        {t("shell.skip")}
      </a>
      <header className="topbar">
        <Link to="/" className="topbar__home" aria-label={t("shell.home")}>
          <Brand />
        </Link>
        <div className="topbar__who">
          {human && (
            <p className="visitor-chip">
              <span className="visually-hidden">{t("shell.visiting")} </span>
              {human.display_name}
            </p>
          )}
          <button
            type="button"
            className="button button--quiet"
            disabled={signOut.isPending}
            onClick={() => {
              signOut.mutate(undefined, {
                onSettled: () => {
                  void navigate("/sign-in");
                },
              });
            }}
          >
            {t("shell.signOut")}
          </button>
        </div>
      </header>
      <main id="main" tabIndex={-1} className="page">
        {children}
      </main>
    </>
  );
}
