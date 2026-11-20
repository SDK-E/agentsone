// SPDX-License-Identifier: MIT
import { useQuery } from "@tanstack/react-query";
import { useEffect, useId, useRef, useState, type KeyboardEvent, type SyntheticEvent } from "react";
import { useTranslation } from "react-i18next";
import { Link, useLocation, useParams } from "react-router";

import { ApiError, type Assignment, type ChatMessage, type LiveEntry } from "../api/api";
import { useApi } from "../app/api-context";
import type { ChatLocationState } from "../router/talk-box";
import { useMe } from "../session/session";
import { problemText } from "../shell/problem-text";
import { useStale } from "../shell/stale";
import { LoadError, Loading } from "../shell/status";
import { Presence } from "../society/agent-presence";
import { liveKey } from "../society/use-live-list";
import { useAgentActivity, useChat } from "./use-chat";

/** One agent's chat. Reached from the router or straight from the society. */
export function ChatView() {
  const { t } = useTranslation();
  const api = useApi();
  const { name = "" } = useParams();
  const location = useLocation();
  const stale = useStale();
  const assignment = (location.state as ChatLocationState | null)?.assignment;
  const live = useQuery({ queryKey: liveKey, queryFn: () => api.listLiveList(), retry: false });
  const entry = live.data?.find((e) => e.agent === name);
  const displayName = entry?.display_name ?? name;
  const activity = useAgentActivity(name, entry);
  const { messages, send, drafts, polling } = useChat(name);
  const me = useMe();
  const myName = me.data?.kind === "human" ? me.data.human.name : undefined;
  const state = activity.state;

  return (
    <div className="chat">
      <nav aria-label={t("chat.navLabel")}>
        <Link to="/" className="back-link">
          {t("chat.back")}
        </Link>
      </nav>
      <header className="chat__header">
        <Presence name={displayName} state={state ?? "sleeping"} />
        <div>
          <h1>{t("chat.title", { name: displayName })}</h1>
          {entry && (
            <p className="resident__meta">
              <span>{t("society.roleInTeam", { role: entry.role, team: entry.team })}</span>
              {state && (
                <span className={`state state--${state}`}>{t(`agent.state.${state}`)}</span>
              )}
              <span>{t(`agent.kind.${entry.kind}`)}</span>
            </p>
          )}
          {entry?.one_line && <p className="resident__line">{entry.one_line.text}</p>}
        </div>
      </header>

      {assignment && (
        <WhyThisAgent
          assignment={assignment}
          displayName={displayName}
          nameOf={(agent) => live.data?.find((e) => e.agent === agent)?.display_name ?? agent}
        />
      )}

      <section aria-labelledby="chat-messages" className="panel chat__messages">
        <h2 id="chat-messages" className="visually-hidden">
          {t("chat.messages")}
        </h2>
        {polling && <p className="hint">{t("chat.polling")}</p>}
        {messages.isPending && <Loading />}
        {messages.isError && (
          <LoadError error={messages.error} onRetry={() => void messages.refetch()} />
        )}
        {messages.isSuccess && messages.data.length === 0 && drafts.length === 0 && (
          <div className="empty">
            <h3>{t("chat.empty.title")}</h3>
            <p>{t("chat.empty.body", { name: displayName })}</p>
          </div>
        )}
        {messages.isSuccess && (messages.data.length > 0 || drafts.length > 0) && (
          <ol className="messages" aria-live="polite">
            {messages.data.map((m) => (
              <Message key={m.id} message={m} me={myName} agentName={displayName} />
            ))}
            {drafts.map((text, i) => (
              <li key={`draft-${String(i)}`} className="message message--agent message--draft">
                <p className="message__author">
                  {t(stale ? "chat.writingPaused" : "chat.writing", { name: displayName })}
                </p>
                <p className="message__text">{text}</p>
              </li>
            ))}
          </ol>
        )}
      </section>

      <Composer
        displayName={displayName}
        pending={send.isPending}
        error={send.error}
        onSend={(text, done) => {
          send.mutate(text, { onSuccess: done });
        }}
      />

      {activity.lines.length > 0 && <Activity entry={entry} lines={activity.lines} />}
    </div>
  );
}

function WhyThisAgent({
  assignment,
  displayName,
  nameOf,
}: {
  assignment: Assignment;
  displayName: string;
  nameOf: (agent: string) => string;
}) {
  const { t } = useTranslation();
  const [best, ...others] = assignment.shortlist;
  return (
    <section aria-labelledby="why-title" className="panel why">
      <h2 id="why-title">{t("chat.why.title", { name: displayName })}</h2>
      {best && <p>{best.reason}</p>}
      {assignment.hops.length > 0 && (
        <ol className="why__hops" aria-label={t("chat.why.hops")}>
          {assignment.hops.map((hop) => (
            <li key={`${hop.router}-${hop.handed_to}`}>
              {t("chat.why.hop", {
                router: nameOf(hop.router),
                to: nameOf(hop.handed_to),
                reason: hop.reason,
              })}
            </li>
          ))}
        </ol>
      )}
      {others.length > 0 && (
        <details>
          <summary>{t("chat.why.others", { count: others.length })}</summary>
          <ul className="why__others">
            {others.map((c) => (
              <li key={c.agent}>
                <Link to={`/agents/${encodeURIComponent(c.agent)}/chat`}>{nameOf(c.agent)}</Link>
                <span>{c.reason}</span>
              </li>
            ))}
          </ul>
        </details>
      )}
      <p className="hint">{t("chat.why.router", { router: nameOf(assignment.router) })}</p>
    </section>
  );
}

function Message({
  message,
  me,
  agentName,
}: {
  message: ChatMessage;
  me: string | undefined;
  agentName: string;
}) {
  const { t, i18n } = useTranslation();
  const time = new Intl.DateTimeFormat(i18n.language, {
    hour: "numeric",
    minute: "2-digit",
  }).format(new Date(message.created_at));
  if (message.role === "system") {
    return (
      <li className="message message--system">
        <p className="message__text">{message.text}</p>
      </li>
    );
  }
  const author =
    message.role === "human"
      ? message.author === `human:${me ?? ""}`
        ? t("chat.you")
        : message.author.replace(/^human:/, "")
      : agentName;
  return (
    <li className={`message message--${message.role}`}>
      <p className="message__author">
        {author} <time dateTime={message.created_at}>{time}</time>
      </p>
      <p className="message__text">{message.text}</p>
    </li>
  );
}

/**
 * Which wall stopped the message, said honestly. The budget is a hard wall. Freezing everything
 * is a hard wall held by the control page; freezing one agent or team is a soft wall in the
 * machine. A 423 does not say which, so the active freezes tell.
 */
function useWallText(error: unknown): string | undefined {
  const { t } = useTranslation();
  const api = useApi();
  const locked = error instanceof ApiError && error.status === 423;
  const freezes = useQuery({
    queryKey: ["freezes"],
    queryFn: () => api.listFreezes(),
    enabled: locked,
    retry: false,
  });
  if (!(error instanceof ApiError)) return undefined;
  if (error.status === 402) return t("walls.budget");
  if (!locked) return undefined;
  return freezes.data?.some((f) => f.scope === "all") === true
    ? t("walls.frozenAll")
    : t("walls.frozen");
}

function Composer({
  displayName,
  pending,
  error,
  onSend,
}: {
  displayName: string;
  pending: boolean;
  error: unknown;
  onSend: (text: string, done: () => void) => void;
}) {
  const { t } = useTranslation();
  const [text, setText] = useState("");
  const errorId = useId();
  const field = useRef<HTMLTextAreaElement>(null);
  const message = text.trim();

  useEffect(() => {
    field.current?.focus();
  }, []);

  const submit = (event?: SyntheticEvent) => {
    event?.preventDefault();
    if (message === "" || pending) return;
    onSend(message, () => {
      setText("");
    });
  };
  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
      event.preventDefault();
      submit();
    }
  };
  const wall = useWallText(error);

  return (
    <form className="panel composer stack-sm" onSubmit={submit}>
      <label className="field">
        <span>{t("chat.composerLabel", { name: displayName })}</span>
        <textarea
          ref={field}
          name="message"
          rows={3}
          maxLength={32000}
          value={text}
          aria-describedby={error ? errorId : undefined}
          onChange={(e) => {
            setText(e.target.value);
          }}
          onKeyDown={onKeyDown}
        />
      </label>
      {error !== null && (
        <div id={errorId} role="alert" className="notice notice--danger">
          <p>{problemText(error, t("chat.sendFailed"))}</p>
          {wall && <p>{wall}</p>}
        </div>
      )}
      <div className="row row--end">
        <p className="hint">{t("chat.enterHint")}</p>
        <button
          type="submit"
          className="button button--primary"
          disabled={message === "" || pending}
        >
          {t("chat.send")}
        </button>
      </div>
    </form>
  );
}

function Activity({
  entry,
  lines,
}: {
  entry: LiveEntry | undefined;
  lines: { text: string; stream: string; time: string }[];
}) {
  const { t } = useTranslation();
  return (
    <section aria-labelledby="activity-title" className="panel activity">
      <h2 id="activity-title">{t("chat.activity", { name: entry?.display_name ?? "" })}</h2>
      <ol className="activity__lines">
        {lines.map((line, i) => (
          <li
            key={`${line.time}-${String(i)}`}
            className={`activity__line activity__line--${line.stream}`}
          >
            {line.text}
          </li>
        ))}
      </ol>
    </section>
  );
}
