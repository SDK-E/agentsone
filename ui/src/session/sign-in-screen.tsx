// SPDX-License-Identifier: MIT
import { useId, useState, type SyntheticEvent } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";

import { problemText } from "../shell/problem-text";
import { Brand } from "../shell/brand";
import { useSignIn } from "./session";

/** Sign in with the human's Linux name and password. */
export function SignInScreen() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const signIn = useSignIn();
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const errorId = useId();

  const submit = (event: SyntheticEvent) => {
    event.preventDefault();
    signIn.mutate(
      { name: name.trim(), password },
      {
        onSuccess: () => {
          void navigate("/");
        },
      },
    );
  };

  return (
    <main id="main" className="entry">
      <div className="entry__card">
        <Brand />
        <h1>{t("signIn.title")}</h1>
        <p className="lead">{t("signIn.lead")}</p>
        <form onSubmit={submit} className="stack" noValidate>
          <label className="field">
            <span>{t("signIn.name")}</span>
            <input
              name="name"
              autoComplete="username"
              autoCapitalize="none"
              spellCheck={false}
              required
              value={name}
              onChange={(e) => {
                setName(e.target.value);
              }}
            />
          </label>
          <label className="field">
            <span>{t("signIn.password")}</span>
            <input
              name="password"
              type="password"
              autoComplete="current-password"
              required
              value={password}
              aria-describedby={signIn.isError ? errorId : undefined}
              onChange={(e) => {
                setPassword(e.target.value);
              }}
            />
          </label>
          {signIn.isError && (
            <p id={errorId} role="alert" className="notice notice--danger">
              {problemText(signIn.error, t("signIn.failed"))}
            </p>
          )}
          <button
            type="submit"
            className="button button--primary"
            disabled={signIn.isPending || name.trim() === "" || password === ""}
          >
            {signIn.isPending ? t("signIn.pending") : t("signIn.submit")}
          </button>
        </form>
      </div>
    </main>
  );
}
