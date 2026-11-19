// SPDX-License-Identifier: MIT
//
// The interface's port to the daemon. Screens depend on the Api interface; the HTTP adapter
// below implements it with the typed contract client, and tests use an in-memory fake.
import {
  createAgentsOneClient,
  isProblem,
  type components,
  type Problem,
} from "@agentsone/contract";

import { readSse } from "./sse";

type S = components["schemas"];
export type SetupStatus = S["SetupStatus"];
export type Human = S["Human"];
export type Principal = S["Principal"];
export type EngineLicence = S["EngineLicence"];
export type EngineFetchStatus = S["EngineFetchStatus"];
export type LiveEntry = S["LiveEntry"];
export type LiveStreamFrame = S["LiveStreamFrame"];
export type Assignment = S["Assignment"];
export type ChatMessage = S["ChatMessage"];
export type ChatStreamFrame = S["ChatStreamFrame"];
export type AgentOutputFrame = S["AgentOutputFrame"];
export type ChartNode = S["ChartNode"];
export type ThemeSelection = S["ThemeSelection"];
export type InstalledTheme = S["InstalledTheme"];
export type FirstHumanCreate = S["FirstHumanCreate"];

/** A failed API call. Carries the RFC 9457 problem when the daemon sent one. */
export class ApiError extends Error {
  readonly status: number;
  readonly problem: Problem | undefined;

  constructor(status: number, problem?: Problem) {
    super(problem?.detail ?? problem?.title ?? `Request failed with status ${String(status)}`);
    this.name = "ApiError";
    this.status = status;
    this.problem = problem;
  }
}

/** A live stream. Call the returned function to close it. */
export type Unsubscribe = () => void;
export interface StreamHandlers<F> {
  onFrame: (frame: F) => void;
  /** The stream could not open or ended. The caller falls back to polling. */
  onClose?: () => void;
}

export interface Api {
  getSetupStatus(): Promise<SetupStatus>;
  createFirstHuman(body: FirstHumanCreate): Promise<Human>;
  getEngineLicence(): Promise<EngineLicence>;
  acceptEngineLicence(version: string): Promise<EngineFetchStatus>;
  getEngineFetchStatus(): Promise<EngineFetchStatus>;
  createSession(name: string, password: string): Promise<Human>;
  deleteSession(): Promise<void>;
  getMe(): Promise<Principal>;
  listLiveList(): Promise<LiveEntry[]>;
  streamLiveList(handlers: StreamHandlers<LiveStreamFrame>): Unsubscribe;
  getChart(): Promise<ChartNode[]>;
  routeRequest(text: string): Promise<Assignment>;
  listChatMessages(agent: string): Promise<ChatMessage[]>;
  sendChatMessage(agent: string, text: string): Promise<ChatMessage>;
  streamChat(agent: string, handlers: StreamHandlers<ChatStreamFrame>): Unsubscribe;
  streamAgentOutput(agent: string, handlers: StreamHandlers<AgentOutputFrame>): Unsubscribe;
  getThemeSelection(): Promise<ThemeSelection>;
  setThemeSelection(update: Partial<ThemeSelection>): Promise<ThemeSelection>;
  getTheme(id: string): Promise<InstalledTheme>;
}

interface Result<T> {
  data?: T;
  error?: unknown;
  response: Response;
}

function unwrap<T>({ data, error, response }: Result<T>): T {
  if (error !== undefined || !response.ok) {
    throw new ApiError(response.status, isProblem(error) ? error : undefined);
  }
  return data as T;
}

export interface HttpApiOptions {
  /** API base ending in /v1. The daemon serves the interface on the same origin. */
  baseUrl?: string;
  fetch?: typeof fetch;
}

/** The real adapter: the typed contract client over same-origin HTTP with the session cookie. */
export function createHttpApi(options: HttpApiOptions = {}): Api {
  const baseUrl = options.baseUrl ?? new URL("/v1", window.location.origin).href;
  const doFetch = options.fetch ?? ((input, init) => fetch(input, init));
  const client = createAgentsOneClient({ baseUrl, fetch: doFetch });

  function stream<F>(path: string, handlers: StreamHandlers<F>): Unsubscribe {
    const abort = new AbortController();
    void (async () => {
      try {
        const response = await doFetch(`${baseUrl}${path}`, {
          headers: { Accept: "text/event-stream" },
          signal: abort.signal,
        });
        if (!response.ok || !response.body) return;
        for await (const message of readSse(response.body)) {
          handlers.onFrame(JSON.parse(message.data) as F);
        }
      } catch {
        // A closed or refused stream is not an error for the person; the caller polls instead.
      } finally {
        if (!abort.signal.aborted) handlers.onClose?.();
      }
    })();
    return () => {
      abort.abort();
    };
  }

  return {
    getSetupStatus: async () => unwrap(await client.GET("/setup")),
    createFirstHuman: async (body) => unwrap(await client.POST("/setup/first-human", { body })),
    getEngineLicence: async () => unwrap(await client.GET("/setup/engine-licence")),
    acceptEngineLicence: async (version) =>
      unwrap(await client.POST("/setup/engine-licence", { body: { accept: true, version } })),
    getEngineFetchStatus: async () => unwrap(await client.GET("/setup/engine-fetch")),
    createSession: async (name, password) =>
      unwrap(await client.POST("/session", { body: { name, password } })).human,
    deleteSession: async () => {
      unwrap(await client.DELETE("/session"));
    },
    getMe: async () => unwrap(await client.GET("/session/me")),
    listLiveList: async () => unwrap(await client.GET("/router/live", {})).items,
    streamLiveList: (handlers) => stream("/router/live/stream", handlers),
    getChart: async () => unwrap(await client.GET("/chart", {})).roots,
    routeRequest: async (text) => unwrap(await client.POST("/router/route", { body: { text } })),
    listChatMessages: async (agent) =>
      unwrap(
        await client.GET("/agents/{agent_name}/chat", { params: { path: { agent_name: agent } } }),
      ).items,
    sendChatMessage: async (agent, text) =>
      unwrap(
        await client.POST("/agents/{agent_name}/chat", {
          params: { path: { agent_name: agent } },
          body: { text },
        }),
      ),
    streamChat: (agent, handlers) =>
      stream(`/agents/${encodeURIComponent(agent)}/chat/stream`, handlers),
    streamAgentOutput: (agent, handlers) =>
      stream(`/agents/${encodeURIComponent(agent)}/output`, handlers),
    getThemeSelection: async () => unwrap(await client.GET("/settings/theme")),
    setThemeSelection: async (update) =>
      unwrap(await client.PUT("/settings/theme", { body: update })),
    getTheme: async (id) =>
      unwrap(await client.GET("/themes/{theme_id}", { params: { path: { theme_id: id } } })),
  };
}
