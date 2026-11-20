// SPDX-License-Identifier: MIT
import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Link, Navigate, useNavigate } from "react-router";

import { isSignedOut, useMe, useSetupStatus, useSignOut } from "../session/session";
import { AccountMenu } from "./account-menu";
import { Brand } from "./brand";
import { StaleContext } from "./stale";
import { LoadError, Loading } from "./status";

/** After this many failed refreshes in a row, a click is asked for as well as the waiting. */
const FAILURES_BEFORE_BUTTON = 3;

/** How many refreshes in a row failed, counted again from zero after one that worked. */
function useFailureStreak(failed: boolean, failedAt: number): number {
  const [streak, setStreak] = useState(0);
  useEffect(() => {
    setStreak((n) => (failed ? n + 1 : 0));
  }, [failed, failedAt]);
  return streak;
}

/**
 * One quiet line, on top of the page and not in its flow, so nothing moves when it comes or goes.
 * The place for it is always there, so a screen reader hears the line when it arrives.
 */
function StaleLine({
  stale,
  failures,
  onRetry,
}: {
  stale: boolean;
  failures: number;
  onRetry: () => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="stale-slot">
      <div role="status" aria-live="polite">
        {stale && (
          <div className="stale">
            <p>{t("status.stale")}</p>
            {failures >= FAILURES_BEFORE_BUTTON && (
              <button type="button" className="button" onClick={onRetry}>
                {t("status.retry")}
              </button>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

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
  // Stale: the last good answers are on screen, and the latest refresh of them failed.
  const setupStale = setup.isError && setup.data !== undefined;
  const meStale = me.isError && me.data !== undefined;
  const stale = setupStale || meStale;
  const setupFailures = useFailureStreak(setupStale, setup.errorUpdatedAt);
  const meFailures = useFailureStreak(meStale, me.errorUpdatedAt);
  const failures = Math.max(setupFailures, meFailures);

  if (setup.data?.phase === "needs-human") return <Navigate to="/setup" replace />;
  if (me.isError && isSignedOut(me.error)) return <Navigate to="/sign-in" replace />;
  if (setup.isPending || me.isPending) {
    return (
      <main id="main" className="entry">
        <Loading label={t("status.waking")} />
      </main>
    );
  }
  // A refresh in the background that fails leaves the last good answer in place; the page stays.
  // Only a first load with nothing to show is an error for the whole screen.
  if ((setup.isError && !setup.data) || (me.isError && !me.data)) {
    const failed = setup.isError && !setup.data ? setup : me;
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
        <AccountMenu
          name={human?.display_name ?? t("shell.someone")}
          signingOut={signOut.isPending}
          onSignOut={() => {
            signOut.mutate(undefined, {
              onSettled: () => {
                void navigate("/sign-in");
              },
            });
          }}
        />
      </header>
      <StaleLine
        stale={stale}
        failures={failures}
        onRetry={() => {
          void setup.refetch();
          void me.refetch();
        }}
      />
      <main id="main" tabIndex={-1} className="page" data-stale={stale || undefined}>
        <StaleContext value={stale}>{children}</StaleContext>
      </main>
    </>
  );
}
