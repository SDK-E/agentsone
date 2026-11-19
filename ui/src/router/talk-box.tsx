// SPDX-License-Identifier: MIT
import { useMutation } from "@tanstack/react-query";
import { useId, useState, type KeyboardEvent, type SyntheticEvent } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";

import type { Assignment } from "../api/api";
import { useApi } from "../app/api-context";
import { problemText } from "../shell/problem-text";

export const MAX_REQUEST = 4000;

export interface ChatLocationState {
  assignment?: Assignment;
}

/**
 * The human's way in: say what you need in your own words. The router reads the live list,
 * picks the best agent and opens its chat, with the reasons shown there.
 */
export function TalkBox() {
  const { t } = useTranslation();
  const api = useApi();
  const navigate = useNavigate();
  const [text, setText] = useState("");
  const hintId = useId();
  const errorId = useId();

  const route = useMutation({
    mutationFn: (request: string) => api.routeRequest(request),
    onSuccess: (assignment) => {
      const state: ChatLocationState = { assignment };
      void navigate(`/agents/${encodeURIComponent(assignment.chosen)}/chat`, { state });
    },
  });

  const request = text.trim();
  const submit = (event?: SyntheticEvent) => {
    event?.preventDefault();
    if (request !== "" && !route.isPending) route.mutate(request);
  };
  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
      event.preventDefault();
      submit();
    }
  };

  return (
    <section aria-labelledby="talk-title" className="panel talk">
      <h2 id="talk-title">{t("talk.title")}</h2>
      <form onSubmit={submit} className="stack">
        <label className="field">
          <span className="visually-hidden">{t("talk.label")}</span>
          <textarea
            name="request"
            rows={4}
            maxLength={MAX_REQUEST}
            placeholder={t("talk.placeholder")}
            aria-describedby={route.isError ? `${hintId} ${errorId}` : hintId}
            value={text}
            onChange={(e) => {
              setText(e.target.value);
            }}
            onKeyDown={onKeyDown}
          />
        </label>
        <p id={hintId} className="hint">
          {t("talk.hint")}
        </p>
        {route.isError && (
          <p id={errorId} role="alert" className="notice notice--danger">
            {problemText(route.error, t("talk.failed"))}
          </p>
        )}
        <div className="row row--end">
          {route.isPending && (
            <p role="status" className="hint">
              {t("talk.pending")}
            </p>
          )}
          <button
            type="submit"
            className="button button--primary"
            disabled={request === "" || route.isPending}
          >
            {t("talk.submit")}
          </button>
        </div>
      </form>
    </section>
  );
}
