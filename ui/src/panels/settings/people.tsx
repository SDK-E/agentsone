// SPDX-License-Identifier: MIT
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useId, useState, type SyntheticEvent } from "react";
import { useTranslation } from "react-i18next";

import type { Human } from "../../api/api";
import { useApi } from "../../app/api-context";
import { useMe } from "../../session/session";
import { problemText } from "../../shell/problem-text";
import { LoadError, Loading } from "../../shell/status";
import { TimeAgo } from "../shared/time-ago";
import { MIN_PASSWORD } from "./password";

function PersonRow({
  person,
  isMe,
  onRemoved,
}: {
  person: Human;
  isMe: boolean;
  onRemoved: (name: string) => void;
}) {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.settings.people" });
  const api = useApi();
  const [confirming, setConfirming] = useState(false);
  const remove = useMutation({
    mutationFn: () => api.deleteHuman(person.name),
    onSuccess: () => {
      onRemoved(person.name);
    },
  });

  return (
    <li className="person">
      <div className="person__head">
        <div className="person__who">
          <strong className="person__display">{person.display_name}</strong>
          <span className="person__name">{person.name}</span>
          {isMe && <span className="chip">{t("you")}</span>}
        </div>
        {!isMe && !confirming && (
          <button
            type="button"
            className="button button--danger"
            aria-label={t("removeLabel", { name: person.name })}
            onClick={() => {
              setConfirming(true);
            }}
          >
            {t("remove")}
          </button>
        )}
      </div>
      <p className="hint">
        {person.last_login_at === null ? (
          t("never")
        ) : (
          <>
            {t("lastSignedIn")} <TimeAgo iso={person.last_login_at} />
          </>
        )}
      </p>
      {confirming && (
        <div className="notice stack-sm">
          <p>{t("removeConfirm", { name: person.name })}</p>
          <div className="row">
            <button
              type="button"
              className="button button--danger"
              disabled={remove.isPending}
              onClick={() => {
                remove.mutate();
              }}
            >
              {remove.isPending ? t("removing") : t("removeYes")}
            </button>
            <button
              type="button"
              className="button"
              onClick={() => {
                setConfirming(false);
                remove.reset();
              }}
            >
              {t("removeNo")}
            </button>
          </div>
        </div>
      )}
      {remove.isError && (
        <p role="alert" className="notice notice--danger">
          {problemText(remove.error, t("removeFailed"))}
        </p>
      )}
    </li>
  );
}

function AddPersonForm({ onAdded }: { onAdded: (name: string) => void }) {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.settings.people.add" });
  const api = useApi();
  const titleId = useId();
  const hintId = useId();
  const [name, setName] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [touched, setTouched] = useState(false);
  const create = useMutation({
    mutationFn: () => api.createHuman({ name, display_name: displayName.trim(), password }),
    onSuccess: () => {
      onAdded(name);
      setName("");
      setDisplayName("");
      setPassword("");
      setConfirm("");
      setTouched(false);
    },
  });

  const problems = [
    !/^[a-z][a-z0-9-]{0,30}$/.test(name) && t("nameRule"),
    displayName.trim() === "" && t("displayNameRule"),
    password.length < MIN_PASSWORD && t("passwordRule", { count: MIN_PASSWORD }),
    password !== confirm && t("confirmRule"),
  ].filter((p): p is string => typeof p === "string");

  const submit = (event: SyntheticEvent) => {
    event.preventDefault();
    setTouched(true);
    if (problems.length === 0) create.mutate();
  };

  return (
    <form aria-labelledby={titleId} className="stack-sm" onSubmit={submit} noValidate>
      <h5 id={titleId}>{t("title")}</h5>
      <div className="stack-sm">
        <label className="field">
          <span>{t("name")}</span>
          <input
            name="name"
            autoComplete="off"
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
          {t("nameHint")}
        </small>
      </div>
      <label className="field">
        <span>{t("displayName")}</span>
        <input
          name="display_name"
          autoComplete="off"
          maxLength={80}
          value={displayName}
          onChange={(e) => {
            setDisplayName(e.target.value);
          }}
        />
      </label>
      <label className="field">
        <span>{t("password")}</span>
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
        <span>{t("confirm")}</span>
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
        <div role="alert" className="notice notice--danger">
          <ul className="notice__list">
            {problems.map((p) => (
              <li key={p}>{p}</li>
            ))}
          </ul>
        </div>
      )}
      {create.isError && (
        <p role="alert" className="notice notice--danger">
          {problemText(create.error, t("failed"))}
        </p>
      )}
      <div className="row">
        <button type="submit" className="button button--primary" disabled={create.isPending}>
          {create.isPending ? t("pending") : t("submit")}
        </button>
      </div>
    </form>
  );
}

/** The humans who can sign in: list, add and remove. Only humans change this list. */
export function PeopleSection() {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.settings.people" });
  const { t: tRoot } = useTranslation();
  const api = useApi();
  const client = useQueryClient();
  const titleId = useId();
  const me = useMe();
  const [notice, setNotice] = useState<string | undefined>();
  const people = useQuery({ queryKey: ["humans"], queryFn: () => api.listHumans(), retry: false });
  const myName = me.data?.kind === "human" ? me.data.human.name : undefined;

  const changed = (message: string) => {
    setNotice(message);
    void client.invalidateQueries({ queryKey: ["humans"] });
  };

  return (
    <section aria-labelledby={titleId} className="stack">
      <h4 id={titleId}>{t("title")}</h4>
      <p className="hint">{t("lead")}</p>
      <p role="status" className="settings__notice">
        {notice}
      </p>

      {people.isPending && <Loading label={t("loading")} />}
      {people.isError && <LoadError error={people.error} onRetry={() => void people.refetch()} />}
      {people.isSuccess && people.data.length === 0 && (
        <div className="empty">
          <h5>{t("empty.title")}</h5>
          <p>{t("empty.body")}</p>
        </div>
      )}
      {people.isSuccess && people.data.length > 0 && (
        <ul className="people" aria-label={t("listLabel")}>
          {people.data.map((person) => (
            <PersonRow
              key={person.name}
              person={person}
              isMe={person.name === myName}
              onRemoved={(name) => {
                changed(t("removed", { name }));
              }}
            />
          ))}
        </ul>
      )}

      <AddPersonForm
        onAdded={(name) => {
          changed(t("added", { name }));
        }}
      />
      <p className="hint">{tRoot("setup.human.softWall")}</p>
    </section>
  );
}
