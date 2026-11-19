// SPDX-License-Identifier: MIT
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useId, useState, type SyntheticEvent } from "react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";

import { ApiError, type Human } from "../../api/api";
import { useApi } from "../../app/api-context";
import { meKey, useMe } from "../../session/session";
import { problemText } from "../../shell/problem-text";
import { LoadError, Loading } from "../../shell/status";
import { MIN_PASSWORD } from "./password";

function DisplayNameForm({ human }: { human: Human }) {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.settings.account" });
  const api = useApi();
  const client = useQueryClient();
  const [value, setValue] = useState(human.display_name);
  const [saved, setSaved] = useState(false);
  const save = useMutation({
    mutationFn: () => api.updateHuman(human.name, { display_name: value.trim() }),
    onSuccess: async () => {
      setSaved(true);
      await client.invalidateQueries({ queryKey: meKey });
      await client.invalidateQueries({ queryKey: ["humans"] });
    },
  });

  const submit = (event: SyntheticEvent) => {
    event.preventDefault();
    if (value.trim() !== "") save.mutate();
  };

  return (
    <form className="stack-sm" onSubmit={submit}>
      <label className="field">
        <span>{t("displayName")}</span>
        <input
          name="display_name"
          autoComplete="name"
          maxLength={80}
          value={value}
          onChange={(e) => {
            setValue(e.target.value);
            setSaved(false);
          }}
        />
      </label>
      <div className="row">
        <button type="submit" className="button" disabled={value.trim() === "" || save.isPending}>
          {save.isPending ? t("savingName") : t("saveName")}
        </button>
        <p role="status" className="hint">
          {saved ? t("nameSaved") : ""}
        </p>
      </div>
      {save.isError && (
        <p role="alert" className="notice notice--danger">
          {problemText(save.error, t("nameFailed"))}
        </p>
      )}
    </form>
  );
}

/** Why a password change failed, in plain words: wrong current password, throttled, or the daemon's own reason. */
function passwordFailure(error: unknown, t: TFunction): string {
  if (error instanceof ApiError) {
    if (error.status === 429) {
      return error.retryAfter === undefined
        ? t("tooManyUnknown")
        : t("tooMany", { count: error.retryAfter });
    }
    if (error.problem?.errors?.some((issue) => issue.pointer === "/current_password") === true) {
      return t("currentWrong");
    }
  }
  return problemText(error, t("passwordFailed"));
}

function PasswordForm({ human }: { human: Human }) {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.settings.account" });
  const api = useApi();
  const hintId = useId();
  const [current, setCurrent] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [touched, setTouched] = useState(false);
  const [changed, setChanged] = useState(false);
  const change = useMutation({
    mutationFn: () => api.updateHuman(human.name, { current_password: current, password }),
    onSuccess: () => {
      setCurrent("");
      setPassword("");
      setConfirm("");
      setTouched(false);
      setChanged(true);
    },
  });

  const problems = [
    current === "" && t("currentRule"),
    password.length < MIN_PASSWORD && t("passwordRule", { count: MIN_PASSWORD }),
    password !== confirm && t("confirmRule"),
  ].filter((p): p is string => typeof p === "string");

  const submit = (event: SyntheticEvent) => {
    event.preventDefault();
    setTouched(true);
    setChanged(false);
    if (problems.length === 0) change.mutate();
  };

  return (
    <form className="stack-sm" onSubmit={submit} noValidate>
      <h5>{t("passwordTitle")}</h5>
      <label className="field">
        <span>{t("currentPassword")}</span>
        <input
          name="current_password"
          type="password"
          autoComplete="current-password"
          value={current}
          onChange={(e) => {
            setCurrent(e.target.value);
          }}
        />
      </label>
      <label className="field">
        <span>{t("newPassword")}</span>
        <input
          name="password"
          type="password"
          autoComplete="new-password"
          aria-describedby={hintId}
          value={password}
          onChange={(e) => {
            setPassword(e.target.value);
          }}
        />
      </label>
      <label className="field">
        <span>{t("confirmPassword")}</span>
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
      <p id={hintId} className="hint">
        {t("passwordHint")}
      </p>
      {touched && problems.length > 0 && (
        <div role="alert" className="notice notice--danger">
          <ul className="notice__list">
            {problems.map((p) => (
              <li key={p}>{p}</li>
            ))}
          </ul>
        </div>
      )}
      {change.isError && (
        <p role="alert" className="notice notice--danger">
          {passwordFailure(change.error, t)}
        </p>
      )}
      <div className="row">
        <button type="submit" className="button button--primary" disabled={change.isPending}>
          {change.isPending ? t("changingPassword") : t("changePassword")}
        </button>
      </div>
      <p role="status" className="hint">
        {changed ? t("passwordChanged") : ""}
      </p>
    </form>
  );
}

/** Who you are, your display name and your password. */
export function AccountSection() {
  const { t } = useTranslation(undefined, { keyPrefix: "panels.settings.account" });
  const titleId = useId();
  const me = useMe();

  return (
    <section aria-labelledby={titleId} className="stack">
      <h4 id={titleId}>{t("title")}</h4>
      {me.isPending && <Loading />}
      {me.isError && <LoadError error={me.error} onRetry={() => void me.refetch()} />}
      {me.isSuccess &&
        (me.data.kind === "human" ? (
          <>
            <dl className="facts">
              <div>
                <dt>{t("name")}</dt>
                <dd>{me.data.human.name}</dd>
              </div>
            </dl>
            <DisplayNameForm key={me.data.human.name} human={me.data.human} />
            <PasswordForm human={me.data.human} />
          </>
        ) : (
          <p className="hint">{t("notHuman")}</p>
        ))}
    </section>
  );
}
