// SPDX-License-Identifier: MIT
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useId, useState, type SyntheticEvent } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";

import type { EngineFetchStatus } from "../api/api";
import { useApi } from "../app/api-context";
import { meKey, setupKey, useSetupStatus } from "../session/session";
import { Brand } from "../shell/brand";
import { LoadError, Loading } from "../shell/status";
import { problemText } from "../shell/problem-text";

const MIN_PASSWORD = 12;

/** First boot: welcome, create the first human, read and accept the engine licence, download. */
export function SetupScreen() {
  const { t } = useTranslation();
  const setup = useSetupStatus();

  let body;
  if (setup.isPending) body = <Loading />;
  else if (setup.isError) {
    body = <LoadError error={setup.error} onRetry={() => void setup.refetch()} />;
  } else {
    switch (setup.data.phase) {
      case "needs-human":
        body = <CreateHuman />;
        break;
      case "needs-licence":
        body = <Licence />;
        break;
      case "fetching-engine":
        body = <EngineFetch initial={setup.data.engine} />;
        break;
      case "ready":
        body = (
          <section aria-labelledby="setup-done" className="stack">
            <h2 id="setup-done">{t("setup.done.title")}</h2>
            <p>{t("setup.done.body")}</p>
            <Link className="button button--primary" to="/">
              {t("setup.goHome")}
            </Link>
          </section>
        );
        break;
    }
  }

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

function Licence() {
  const { t } = useTranslation();
  const api = useApi();
  const client = useQueryClient();
  const licence = useQuery({
    queryKey: ["engine-licence"],
    queryFn: () => api.getEngineLicence(),
    retry: false,
  });
  const accept = useMutation({
    mutationFn: (version: string) => api.acceptEngineLicence(version),
    onSuccess: () => client.invalidateQueries({ queryKey: setupKey }),
  });

  if (licence.isPending) return <Loading />;
  if (licence.isError) {
    return <LoadError error={licence.error} onRetry={() => void licence.refetch()} />;
  }
  return (
    <section aria-labelledby="setup-licence" className="stack">
      <h2 id="setup-licence">{t("setup.licence.title")}</h2>
      <p>{t("setup.licence.body")}</p>
      <article className="licence" aria-label={t("setup.licence.textLabel")}>
        {licence.data.text}
      </article>
      <p className="hint">{t("setup.licence.version", { version: licence.data.version })}</p>
      {accept.isError && (
        <p role="alert" className="notice notice--danger">
          {problemText(accept.error, t("setup.licence.failed"))}
        </p>
      )}
      <div className="row">
        <button
          type="button"
          className="button button--primary"
          disabled={accept.isPending}
          onClick={() => {
            accept.mutate(licence.data.version);
          }}
        >
          {t("setup.licence.accept")}
        </button>
        <Link className="button" to="/">
          {t("setup.licence.later")}
        </Link>
      </div>
      <p className="hint">{t("setup.licence.laterHint")}</p>
    </section>
  );
}

function EngineFetch({ initial }: { initial: EngineFetchStatus }) {
  const { t } = useTranslation();
  const api = useApi();
  const status = useQuery({
    queryKey: ["engine-fetch"],
    queryFn: () => api.getEngineFetchStatus(),
    initialData: initial,
    refetchInterval: (q) =>
      q.state.data?.state === "ready" || q.state.data?.state === "failed" ? false : 2000,
  });
  const engine = status.data;
  const mb = (bytes: number | null) => (bytes === null ? "?" : String(Math.round(bytes / 1e6)));
  return (
    <section aria-labelledby="setup-engine" className="stack">
      <h2 id="setup-engine">{t("setup.engine.title")}</h2>
      <p>{t(`setup.engine.state.${engine.state}`)}</p>
      <progress
        className="progress"
        max={1}
        value={engine.progress}
        aria-label={t("setup.engine.progressLabel")}
      />
      <p className="hint">
        {t("setup.engine.bytes", { done: mb(engine.bytes_done), total: mb(engine.bytes_total) })}
      </p>
      {engine.error !== null && (
        <p role="alert" className="notice notice--danger">
          {engine.error}
        </p>
      )}
      <p>{t("setup.engine.meanwhile")}</p>
      <Link className="button button--primary" to="/">
        {t("setup.goHome")}
      </Link>
    </section>
  );
}
