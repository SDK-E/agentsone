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
export type Freeze = S["Freeze"];
// Monitor, tasks and audit
export type Budget = S["Budget"];
export type BudgetSplit = S["BudgetSplit"];
export type BudgetNode = S["BudgetNode"];
export type MachineMetrics = S["MachineMetrics"];
export type Task = S["Task"];
export type TaskCreate = S["TaskCreate"];
export type TaskStatus = S["TaskStatus"];
export type TaskStreamFrame = S["TaskStreamFrame"];
export type Schedule = S["Schedule"];
export type AuditRecord = S["AuditRecord"];
export type AuditVerification = S["AuditVerification"];

/** One page of a list, with the cursor for the next page when there is one. */
export interface Page<T> {
  items: T[];
  next_cursor: string | null;
}

export interface AuditQuery {
  after_seq?: number;
  limit?: number;
}

export interface TaskFilter {
  status?: TaskStatus;
  assignee?: string;
}

// Memory, plugins and settings
export type MemoryRecord = S["MemoryRecord"];
export type MemoryScope = S["MemoryScope"];
export type MemoryHit = S["MemoryHit"];
export type Plugin = S["Plugin"];
export type PluginKind = S["PluginKind"];
export type PluginSource = S["PluginSource"];
export type PluginSourceId = S["PluginSourceId"];
export type CatalogEntry = S["CatalogEntry"];
export type HumanCreate = S["HumanCreate"];
export type HumanUpdate = S["HumanUpdate"];

/** One page of a list, with the cursor for the next page (null on the last page). */
export interface Page<T> {
  items: T[];
  next_cursor: string | null;
}

/** A failed API call. Carries the RFC 9457 problem when the daemon sent one. */
export class ApiError extends Error {
  readonly status: number;
  readonly problem: Problem | undefined;
  /** Seconds to wait, from the Retry-After header of a 429. */
  readonly retryAfter: number | undefined;

  constructor(status: number, problem?: Problem, retryAfter?: number) {
    super(problem?.detail ?? problem?.title ?? `Request failed with status ${String(status)}`);
    this.name = "ApiError";
    this.status = status;
    this.problem = problem;
    this.retryAfter = retryAfter;
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
  listFreezes(): Promise<Freeze[]>;
  // monitor, tasks and audit
  getBudget(): Promise<Budget>;
  getBudgetSplit(): Promise<BudgetSplit>;
  getMachineMetrics(): Promise<MachineMetrics>;
  listTasks(filter: TaskFilter, cursor?: string): Promise<Page<Task>>;
  getTask(id: string): Promise<Task>;
  createTask(body: TaskCreate): Promise<Task>;
  streamTasks(handlers: StreamHandlers<TaskStreamFrame>): Unsubscribe;
  listSchedules(): Promise<Schedule[]>;
  /** Records come oldest first: ask for those after a sequence number to reach the newest. */
  listAuditRecords(query?: AuditQuery): Promise<Page<AuditRecord>>;
  verifyAuditChain(): Promise<AuditVerification>;

  // memory, plugins and settings
  listMemory(query: { scope?: MemoryScope; cursor?: string }): Promise<Page<MemoryRecord>>;
  searchMemory(query: {
    q: string;
    scope?: MemoryScope;
    cursor?: string;
  }): Promise<Page<MemoryHit>>;
  deleteMemory(id: string): Promise<void>;
  listPlugins(query?: { kind?: PluginKind; cursor?: string }): Promise<Page<Plugin>>;
  listPluginSources(): Promise<PluginSource[]>;
  searchPluginCatalog(query: {
    q?: string;
    source?: PluginSourceId;
    kind?: PluginKind;
    cursor?: string;
  }): Promise<Page<CatalogEntry>>;
  installPlugin(body: { source: PluginSourceId; ref: string }): Promise<Plugin>;
  uninstallPlugin(id: string): Promise<void>;
  enablePlugin(id: string): Promise<Plugin>;
  disablePlugin(id: string): Promise<Plugin>;
  listHumans(): Promise<Human[]>;
  createHuman(body: HumanCreate): Promise<Human>;
  updateHuman(name: string, body: HumanUpdate): Promise<Human>;
  deleteHuman(name: string): Promise<void>;
  listThemes(): Promise<InstalledTheme[]>;
}

interface Result<T> {
  data?: T;
  error?: unknown;
  response: Response;
}

function unwrap<T>({ data, error, response }: Result<T>): T {
  if (error !== undefined || !response.ok) {
    const wait = Number(response.headers.get("Retry-After"));
    throw new ApiError(
      response.status,
      isProblem(error) ? error : undefined,
      Number.isInteger(wait) && wait > 0 ? wait : undefined,
    );
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
    listFreezes: async () => unwrap(await client.GET("/host/freezes")).items,
    // monitor, tasks and audit
    getBudget: async () => unwrap(await client.GET("/budget")),
    getBudgetSplit: async () => unwrap(await client.GET("/budget/split")),
    getMachineMetrics: async () => unwrap(await client.GET("/machine/metrics")),
    listTasks: async (filter, cursor) =>
      unwrap(
        await client.GET("/tasks", {
          params: {
            query: {
              ...(filter.status !== undefined && { status: filter.status }),
              ...(filter.assignee !== undefined && { assignee: filter.assignee }),
              ...(cursor !== undefined && { cursor }),
            },
          },
        }),
      ),
    getTask: async (id) =>
      unwrap(await client.GET("/tasks/{task_id}", { params: { path: { task_id: id } } })),
    createTask: async (body) => unwrap(await client.POST("/tasks", { body })),
    streamTasks: (handlers) => stream("/tasks/stream", handlers),
    listSchedules: async () => unwrap(await client.GET("/schedules", {})).items,
    listAuditRecords: async (query = {}) =>
      unwrap(
        await client.GET("/audit", {
          params: {
            query: {
              ...(query.after_seq !== undefined && { after_seq: query.after_seq }),
              ...(query.limit !== undefined && { limit: query.limit }),
            },
          },
        }),
      ),
    verifyAuditChain: async () => unwrap(await client.POST("/audit/verify", { body: {} })),

    // memory, plugins and settings
    listMemory: async ({ scope, cursor }) =>
      unwrap(
        await client.GET("/memory", {
          params: { query: { ...(scope && { scope }), ...(cursor && { cursor }) } },
        }),
      ),
    searchMemory: async ({ q, scope, cursor }) =>
      unwrap(
        await client.GET("/memory/search", {
          params: { query: { q, ...(scope && { scope }), ...(cursor && { cursor }) } },
        }),
      ),
    deleteMemory: async (id) => {
      unwrap(await client.DELETE("/memory/{memory_id}", { params: { path: { memory_id: id } } }));
    },
    listPlugins: async ({ kind, cursor } = {}) =>
      unwrap(
        await client.GET("/plugins", {
          params: { query: { ...(kind && { kind }), ...(cursor && { cursor }) } },
        }),
      ),
    listPluginSources: async () => unwrap(await client.GET("/plugin-sources", {})).items,
    searchPluginCatalog: async ({ q, source, kind, cursor }) =>
      unwrap(
        await client.GET("/plugin-catalog", {
          params: {
            query: {
              ...(q && { q }),
              ...(source && { source }),
              ...(kind && { kind }),
              ...(cursor && { cursor }),
            },
          },
        }),
      ),
    installPlugin: async ({ source, ref }) =>
      unwrap(await client.POST("/plugins", { body: { source, ref, enable: true } })),
    uninstallPlugin: async (id) => {
      unwrap(await client.DELETE("/plugins/{plugin_id}", { params: { path: { plugin_id: id } } }));
    },
    enablePlugin: async (id) =>
      unwrap(
        await client.POST("/plugins/{plugin_id}/enable", { params: { path: { plugin_id: id } } }),
      ),
    disablePlugin: async (id) =>
      unwrap(
        await client.POST("/plugins/{plugin_id}/disable", { params: { path: { plugin_id: id } } }),
      ),
    listHumans: async () => unwrap(await client.GET("/humans", {})).items,
    createHuman: async (body) => unwrap(await client.POST("/humans", { body })),
    updateHuman: async (name, body) =>
      unwrap(
        await client.PATCH("/humans/{human_name}", {
          params: { path: { human_name: name } },
          body,
        }),
      ),
    deleteHuman: async (name) => {
      unwrap(
        await client.DELETE("/humans/{human_name}", { params: { path: { human_name: name } } }),
      );
    },
    listThemes: async () => unwrap(await client.GET("/themes", {})).items,
  };
}
