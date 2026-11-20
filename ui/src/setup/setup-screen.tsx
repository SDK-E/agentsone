// SPDX-License-Identifier: MIT
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useId, useState, type SyntheticEvent } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

import { useApi } from "../app/api-context";
import { meKey, setupKey, useSetupStatus } from "../session/session";
import { Brand } from "../shell/brand";
import { LoadError, Loading } from "../shell/status";
import { problemText } from "../shell/problem-text";

const MIN_PASSWORD = 12;

/**
 * First boot: welcome and create the first human. That is all of setup: the Claude engine is
 * off by default and turned on later in Settings, so setup never shows its licence.
 */
export function SetupScreen() {
  const { t } = useTranslation();
  const setup = useSetupStatus();

  let body;
  if (setup.isPending) body = <Loading />;
  else if (setup.isError) {
    body = <LoadError error={setup.error} onRetry={() => void setup.refetch()} />;
  } else if (setup.data.phase === "needs-human") body = <CreateHuman />;
  // An older daemon may still answer needs-licence or fetching-engine; setup is done anyway.
  else body = <Done engineOn={setup.data.licence_accepted} />;

  return (
    <main id="main" className="entry">
      <div className="entry__card entry__card--wide">
        <Brand />
        <h1>{t("setup.title")}</h1>
        {body}
      </div>
    </main>
  );
}

function Done({ engineOn }: { engineOn: boolean }) {
  const { t } = useTranslation();
  return (
    <section aria-labelledby="setup-done" className="stack">
      <h2 id="setup-done">{t("setup.done.title")}</h2>
      <p>{t("setup.done.body")}</p>
      {!engineOn && <p className="hint">{t("setup.done.engineOff")}</p>}
      <Link className="button button--primary" to="/">
        {t("setup.goHome")}
      </Link>
    </section>
  );
}

function CreateHuman() {
  const { t } = useTranslation();
  const api = useApi();
  const client = useQueryClient();
  const [name, setName] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [touched, setTouched] = useState(false);
  const hintId = useId();
  const errorId = useId();

  const create = useMutation({
    mutationFn: async () => {
      await api.createFirstHuman({ name, display_name: displayName.trim(), password });
      await api.createSession(name, password);
    },
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: setupKey });
      await client.invalidateQueries({ queryKey: meKey });
    },
  });

  const nameValid = /^[a-z][a-z0-9-]{0,30}$/.test(name);
  const problems = [
    !nameValid && t("setup.human.nameRule"),
    displayName.trim() === "" && t("setup.human.displayNameRule"),
    password.length < MIN_PASSWORD && t("setup.human.passwordRule", { count: MIN_PASSWORD }),
    password !== confirm && t("setup.human.confirmRule"),
  ].filter((p): p is string => typeof p === "string");

  const submit = (event: SyntheticEvent) => {
    event.preventDefault();
    setTouched(true);
    if (problems.length === 0) create.mutate();
  };

  return (
    <section aria-labelledby="setup-human" className="stack">
      <p className="lead">{t("setup.welcome")}</p>
      <h2 id="setup-human">{t("setup.human.title")}</h2>
      <p>{t("setup.human.body")}</p>
      <form onSubmit={submit} className="stack" noValidate>
        <div className="field">
          <label className="field">
            <span>{t("setup.human.name")}</span>
            <input
              name="name"
              autoComplete="username"
              autoCapitalize="none"
              spellCheck={false}
              aria-describedby={hintId}
              value={name}
              onChange={(e) => {
                setName(e.target.value.toLowerCase());
              }}
            />
          </label>
          <small id={hintId} className="hint">
            {t("setup.human.nameHint")}
          </small>
        </div>
        <label className="field">
          <span>{t("setup.human.displayName")}</span>
          <input
            name="display_name"
            autoComplete="name"
            value={displayName}
            onChange={(e) => {
              setDisplayName(e.target.value);
            }}
          />
        </label>
        <label className="field">
          <span>{t("setup.human.password")}</span>
          <input
            name="password"
            type="password"
            autoComplete="new-password"
            value={password}
            onChange={(e) => {
              setPassword(e.target.value);
            }}
          />
        </label>
        <label className="field">
          <span>{t("setup.human.confirm")}</span>
          <input
            name="confirm"
            type="password"
            autoComplete="new-password"
            value={confirm}
            onChange={(e) => {
              setConfirm(e.target.value);
            }}
          />
        </label>
        {touched && problems.length > 0 && (
          <ul id={errorId} role="alert" className="notice notice--danger">
            {problems.map((p) => (
              <li key={p}>{p}</li>
            ))}
          </ul>
        )}
        {create.isError && (
          <p role="alert" className="notice notice--danger">
            {problemText(create.error, t("setup.human.failed"))}
          </p>
        )}
        <p className="hint">{t("setup.human.softWall")}</p>
        <button type="submit" className="button button--primary" disabled={create.isPending}>
          {create.isPending ? t("setup.human.pending") : t("setup.human.submit")}
        </button>
      </form>
    </section>
  );
}
