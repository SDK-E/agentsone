// SPDX-License-Identifier: MIT
//
// An in-memory Api for component and page tests, seeded from the contract's golden fixtures so
// tests see the same data as the fake daemon.
import { render } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter } from "react-router";

import chart from "../../../contract/fixtures/api/getChart.json";
import licence from "../../../contract/fixtures/api/getEngineLicence.json";
import engineFetch from "../../../contract/fixtures/api/getEngineFetchStatus.json";
import me from "../../../contract/fixtures/api/getMe.json";
import setup from "../../../contract/fixtures/api/getSetupStatus.json";
// Monitor, tasks and audit
import auditList from "../../../contract/fixtures/api/listAuditRecords.json";
import budget from "../../../contract/fixtures/api/getBudget.json";
import budgetSplit from "../../../contract/fixtures/api/getBudgetSplit.json";
import createdTask from "../../../contract/fixtures/api/createTask.json";
import machineMetrics from "../../../contract/fixtures/api/getMachineMetrics.json";
import schedulesList from "../../../contract/fixtures/api/listSchedules.json";
import tasksList from "../../../contract/fixtures/api/listTasks.json";
import verification from "../../../contract/fixtures/api/verifyAuditChain.json";
import theme from "../../../contract/fixtures/api/getTheme.json";
import failedAgent from "../../../contract/fixtures/api/getAgent.failed.json";
import failedAgentLong from "../../../contract/fixtures/api/getAgent.failed-long-text.json";
import chat from "../../../contract/fixtures/api/listChatMessages.json";
import humans from "../../../contract/fixtures/api/listHumans.json";
import live from "../../../contract/fixtures/api/listLiveList.json";
import memory from "../../../contract/fixtures/api/listMemory.json";
import memoryLong from "../../../contract/fixtures/api/listMemory.long-text.json";
import pluginSources from "../../../contract/fixtures/api/listPluginSources.json";
import plugins from "../../../contract/fixtures/api/listPlugins.json";
import themes from "../../../contract/fixtures/api/listThemes.json";
import memoryHits from "../../../contract/fixtures/api/searchMemory.json";
import catalog from "../../../contract/fixtures/api/searchPluginCatalog.json";
import route from "../../../contract/fixtures/api/routeRequest.json";
// Machine
import filesList from "../../../contract/fixtures/api/listFiles.json";
import fileRead from "../../../contract/fixtures/api/readFile.json";
import machineUsers from "../../../contract/fixtures/api/listMachineUsers.json";
import packagesList from "../../../contract/fixtures/api/listPackages.json";
import processesList from "../../../contract/fixtures/api/listProcesses.json";
import ptyCreated from "../../../contract/fixtures/api/createPtySession.json";
import ptyList from "../../../contract/fixtures/api/listPtySessions.json";
import servicesList from "../../../contract/fixtures/api/listServices.json";
import routeError from "../../../contract/fixtures/api/routeRequest.error.json";
import {
  ApiError,
  type Agent,
  type AgentOutputFrame,
  type Api,
  type Assignment,
  type AuditQuery,
  type AuditRecord,
  type AuditVerification,
  type Budget,
  type BudgetSplit,
  type CatalogEntry,
  type ChartNode,
  type ChatMessage,
  type ChatStreamFrame,
  type EngineFetchStatus,
  type EngineLicence,
  type FileContent,
  type FileList,
  type Freeze,
  type Human,
  type InstalledTheme,
  type LiveEntry,
  type LiveStreamFrame,
  type MachineMetrics,
  type MachinePackage,
  type MachineProcess,
  type MachineService,
  type MachineUser,
  type MemoryHit,
  type MemoryRecord,
  type Page,
  type Plugin,
  type PluginSource,
  type Principal,
  type PtySession,
  type Schedule,
  type SetupStatus,
  type StreamHandlers,
  type Task,
  type TaskCreate,
  type TaskFilter,
  type TaskStreamFrame,
} from "../api/api";
import { AppProviders, AppRoutes } from "../app/app";

export const fixtures = {
  setup: setup.body as SetupStatus,
  me: me.body as Principal,
  live: live.body.items as LiveEntry[],
  failedAgent: failedAgent.body as Agent,
  failedAgentLong: failedAgentLong.body as Agent,
  chart: chart.body.roots as ChartNode[],
  chat: chat.body.items as ChatMessage[],
  route: route.body as Assignment,
  routeError: new ApiError(503, routeError.body),
  licence: licence.body as EngineLicence,
  engineFetch: engineFetch.body as EngineFetchStatus,
  theme: theme.body as InstalledTheme,
  // monitor, tasks and audit
  budget: budget.body as Budget,
  split: budgetSplit.body as BudgetSplit,
  metrics: machineMetrics.body satisfies MachineMetrics,
  tasks: tasksList.body.items as Task[],
  newTask: createdTask.body as Task,
  schedules: schedulesList.body.items as Schedule[],
  // The generated type of `details` is `Record<string, never>`; the fixtures carry real values.
  audit: auditList.body.items as unknown as AuditRecord[],
  verification: verification.body as AuditVerification,
  // memory, plugins and settings
  memory: memory.body.items as MemoryRecord[],
  memoryLong: memoryLong.body.items as MemoryRecord[],
  memoryHits: memoryHits.body.items as MemoryHit[],
  plugins: plugins.body.items as Plugin[],
  pluginSources: pluginSources.body.items as PluginSource[],
  catalog: catalog.body.items as CatalogEntry[],
  humans: humans.body.items as Human[],
  themes: themes.body.items as InstalledTheme[],
  // machine
  processes: processesList.body.items as MachineProcess[],
  services: servicesList.body.items as MachineService[],
  packages: packagesList.body.items as MachinePackage[],
  machineUsers: machineUsers.body.items as MachineUser[],
  files: filesList.body as FileList,
  file: fileRead.body as FileContent,
  ptys: ptyList.body.items as PtySession[],
  newPty: ptyCreated.body as PtySession,
};

/** Cut a list into pages the way the daemon does: the cursor is where the next page starts. */
function paged<T>(items: T[], cursor: string | undefined, size: number): Page<T> {
  const start = cursor === undefined ? 0 : Number(cursor);
  const end = start + size;
  return { items: items.slice(start, end), next_cursor: end < items.length ? String(end) : null };
}

/** A failed call as the daemon reports it: an RFC 9457 problem. */
export const problem = (status: number, title: string, detail?: string) =>
  new ApiError(status, {
    type: "about:blank",
    title,
    status,
    ...(detail !== undefined && { detail }),
  });

export const unauthorized = () => problem(401, "Sign in first");

/** A stream the test drives by hand. */
export class FakeStream<F> {
  handlers: StreamHandlers<F> | undefined;
  open(handlers: StreamHandlers<F>) {
    this.handlers = handlers;
    return () => {
      this.handlers = undefined;
    };
  }
  emit(frame: F) {
    this.handlers?.onFrame(frame);
  }
  close() {
    this.handlers?.onClose?.();
  }
}

export class FakeApi implements Api {
  setup = { ...fixtures.setup };
  me: Principal | ApiError = fixtures.me;
  live: LiveEntry[] | ApiError = [...fixtures.live];
  chart: ChartNode[] | ApiError = fixtures.chart;
  chat: ChatMessage[] | ApiError = [...fixtures.chat];
  route: Assignment | ApiError = fixtures.route;
  sendError: ApiError | undefined;
  liveStream = new FakeStream<LiveStreamFrame>();
  chatStream = new FakeStream<ChatStreamFrame>();
  outputStream = new FakeStream<AgentOutputFrame>();
  calls: string[] = [];

  private settle<T>(value: T | ApiError): Promise<T> {
    return value instanceof ApiError ? Promise.reject(value) : Promise.resolve(value);
  }

  getSetupStatus = () => Promise.resolve(this.setup);
  createFirstHuman = (body: { name: string; display_name: string }) => {
    this.calls.push(`createFirstHuman ${body.name}`);
    this.setup = { ...this.setup, phase: "needs-licence", first_human_created: true };
    return Promise.resolve({
      name: body.name,
      display_name: body.display_name,
      uid: 1000,
      created_at: "2026-11-19T07:59:12Z",
      last_login_at: null,
    });
  };
  /** The engine licence; not accepted until a test or the person accepts it. */
  licence: EngineLicence | ApiError = {
    ...fixtures.licence,
    accepted: false,
    accepted_at: null,
    accepted_by: null,
  };
  engine: EngineFetchStatus | ApiError = fixtures.engineFetch;
  getEngineLicence = () => this.settle(this.licence);
  acceptEngineLicence = (version: string) => {
    this.calls.push(`acceptEngineLicence ${version}`);
    this.setup = { ...this.setup, phase: "fetching-engine", engine: fixtures.engineFetch };
    if (!(this.licence instanceof ApiError)) {
      this.licence = {
        ...this.licence,
        accepted: true,
        accepted_at: "2026-11-19T20:00:00Z",
        accepted_by: "ada",
      };
    }
    return Promise.resolve(fixtures.engineFetch);
  };
  getEngineFetchStatus = () => this.settle(this.engine);
  createSession = (name: string) => {
    this.calls.push(`createSession ${name}`);
    if (name === "wrong") {
      return Promise.reject(problem(401, "Wrong", "Name or password is wrong."));
    }
    this.me = fixtures.me;
    return Promise.resolve(meHuman());
  };
  deleteSession = () => {
    this.calls.push("deleteSession");
    this.me = unauthorized();
    return Promise.resolve();
  };
  getMe = () => this.settle(this.me);
  listLiveList = () => this.settle(this.live);
  streamLiveList = (h: StreamHandlers<LiveStreamFrame>) => this.liveStream.open(h);
  /** Agents by name, for the reads that need more than the live list carries. */
  agents: Record<string, Agent> = {};
  getAgent = (agent: string) => {
    this.calls.push(`getAgent ${agent}`);
    const found = this.agents[agent];
    return found ? Promise.resolve(found) : Promise.reject(problem(404, "Agent not found"));
  };
  getChart = () => this.settle(this.chart);
  routeRequest = (text: string) => {
    this.calls.push(`routeRequest ${text}`);
    return this.settle(this.route);
  };
  listChatMessages = () => this.settle(this.chat);
  sendChatMessage = (agent: string, text: string) => {
    this.calls.push(`sendChatMessage ${agent} ${text}`);
    if (this.sendError) return Promise.reject(this.sendError);
    return Promise.resolve<ChatMessage>({
      id: "cht_01JD7ZK3M00000000000000099",
      agent,
      role: "human",
      author: "human:ada",
      text,
      assignment_id: null,
      created_at: "2026-11-19T19:00:00Z",
    });
  };
  streamChat = (_agent: string, h: StreamHandlers<ChatStreamFrame>) => this.chatStream.open(h);
  streamAgentOutput = (_agent: string, h: StreamHandlers<AgentOutputFrame>) =>
    this.outputStream.open(h);
  selection: { theme_id: string; mode: "system" | "light" | "dark" } = {
    theme_id: fixtures.theme.id,
    mode: "system",
  };
  getThemeSelection = () => Promise.resolve({ ...this.selection });
  setThemeSelection = (update: { theme_id?: string; mode?: "system" | "light" | "dark" }) =>
    this.change(`setThemeSelection ${update.mode ?? ""}${update.theme_id ?? ""}`, () => {
      this.selection = { ...this.selection, ...update };
      return { ...this.selection };
    });
  getTheme = () => Promise.resolve(fixtures.theme);
  freezes: Freeze[] = [];
  listFreezes = () => Promise.resolve(this.freezes);

  // monitor, tasks and audit
  budget: Budget | ApiError = fixtures.budget;
  split: BudgetSplit | ApiError = fixtures.split;
  metrics: MachineMetrics | ApiError = fixtures.metrics;
  tasks: Task[] | ApiError = [...fixtures.tasks];
  schedules: Schedule[] | ApiError = fixtures.schedules;
  /** Every audit record, oldest first. Served the way the daemon does: after a sequence number. */
  audit: AuditRecord[] | ApiError = [...fixtures.audit];
  verification: AuditVerification | ApiError = fixtures.verification;
  createError: ApiError | undefined;
  taskStream = new FakeStream<TaskStreamFrame>();
  getBudget = () => this.settle(this.budget);
  getBudgetSplit = () => this.settle(this.split);
  getMachineMetrics = () => this.settle(this.metrics);
  listTasks = (filter: TaskFilter, cursor?: string) => {
    this.calls.push(
      `listTasks ${filter.status ?? "any"} ${filter.assignee ?? "anyone"} ${cursor ?? ""}`.trim(),
    );
    if (this.tasks instanceof ApiError) return Promise.reject(this.tasks);
    const items = this.tasks.filter(
      (t) =>
        (filter.status === undefined || t.status === filter.status) &&
        (filter.assignee === undefined || t.assignee === filter.assignee),
    );
    return Promise.resolve<Page<Task>>({ items, next_cursor: null });
  };
  getTask = (id: string) => {
    if (this.tasks instanceof ApiError) return Promise.reject(this.tasks);
    const found = this.tasks.find((t) => t.id === id);
    return found ? Promise.resolve(found) : Promise.reject(problem(404, "Task not found"));
  };
  createTask = (body: TaskCreate) => {
    this.calls.push(`createTask ${body.title} ${body.assignee ?? ""}`.trim());
    if (this.createError) return Promise.reject(this.createError);
    const task: Task = {
      ...fixtures.newTask,
      title: body.title,
      detail: body.detail ?? "",
      priority: body.priority ?? "normal",
      assignee: body.assignee ?? null,
    };
    if (!(this.tasks instanceof ApiError)) this.tasks = [task, ...this.tasks];
    return Promise.resolve(task);
  };
  streamTasks = (h: StreamHandlers<TaskStreamFrame>) => this.taskStream.open(h);
  listSchedules = () => this.settle(this.schedules);
  listAuditRecords = (query: AuditQuery = {}) => {
    const after = query.after_seq ?? 0;
    const limit = query.limit ?? 50;
    this.calls.push(`listAuditRecords after=${String(after)} limit=${String(limit)}`);
    if (this.audit instanceof ApiError) return Promise.reject(this.audit);
    const items = this.audit.filter((r) => r.seq > after).slice(0, limit);
    return Promise.resolve<Page<AuditRecord>>({ items, next_cursor: null });
  };
  verifyAuditChain = () => {
    this.calls.push("verifyAuditChain");
    return this.settle(this.verification);
  };

  // memory, plugins and settings
  pageSize = 1000;
  memory: MemoryRecord[] | ApiError = [...fixtures.memory];
  memoryHits: MemoryHit[] = [...fixtures.memoryHits];
  plugins: Plugin[] | ApiError = [...fixtures.plugins];
  pluginSources: PluginSource[] | ApiError = [...fixtures.pluginSources];
  catalog: CatalogEntry[] | ApiError = [...fixtures.catalog];
  humans: Human[] | ApiError = [...fixtures.humans];
  themes: InstalledTheme[] = [fixtures.theme];
  /** The next change (install, delete, turn off and so on) fails with this. */
  mutationError: ApiError | undefined;

  private change<T>(call: string, run: () => T): Promise<T> {
    this.calls.push(call);
    if (this.mutationError) return Promise.reject(this.mutationError);
    try {
      return Promise.resolve(run());
    } catch (error) {
      return Promise.reject(error instanceof Error ? error : new Error(String(error)));
    }
  }

  listMemory = ({ scope, cursor }: { scope?: string; cursor?: string }) =>
    this.settle(this.memory).then((all) =>
      paged(
        all.filter((m) => scope === undefined || m.scope === scope),
        cursor,
        this.pageSize,
      ),
    );
  searchMemory = ({ q, scope, cursor }: { q: string; scope?: string; cursor?: string }) => {
    this.calls.push(`searchMemory ${q}`);
    return Promise.resolve(
      paged(
        this.memoryHits.filter((h) => scope === undefined || h.record.scope === scope),
        cursor,
        this.pageSize,
      ),
    );
  };
  deleteMemory = (id: string) =>
    this.change(`deleteMemory ${id}`, () => {
      if (!(this.memory instanceof ApiError)) this.memory = this.memory.filter((m) => m.id !== id);
    });
  listPlugins = ({ kind, cursor }: { kind?: string; cursor?: string } = {}) =>
    this.settle(this.plugins).then((all) =>
      paged(
        all.filter((p) => kind === undefined || p.kinds.some((k) => k === kind)),
        cursor,
        this.pageSize,
      ),
    );
  listPluginSources = () => this.settle(this.pluginSources);
  searchPluginCatalog = ({
    q,
    source,
    cursor,
  }: {
    q?: string;
    source?: string;
    kind?: string;
    cursor?: string;
  }) => {
    this.calls.push(`searchPluginCatalog ${q ?? ""} ${source ?? ""}`.trimEnd());
    return this.settle(this.catalog).then((all) =>
      paged(
        all.filter(
          (e) =>
            (source === undefined || e.source === source) &&
            (q === undefined || q === "" || `${e.name} ${e.summary}`.includes(q)),
        ),
        cursor,
        this.pageSize,
      ),
    );
  };
  private findPlugin(id: string): Plugin {
    const found =
      this.plugins instanceof ApiError ? undefined : this.plugins.find((p) => p.id === id);
    if (!found) throw problem(404, "Not found", `No plugin ${id} is installed.`);
    return found;
  }
  private setEnabled(id: string, enabled: boolean): Plugin {
    const next = { ...this.findPlugin(id), enabled };
    if (!(this.plugins instanceof ApiError)) {
      this.plugins = this.plugins.map((p) => (p.id === id ? next : p));
    }
    return next;
  }
  installPlugin = ({ source, ref }: { source: string; ref: string }) =>
    this.change(`installPlugin ${source} ${ref}`, () => {
      const entry = fixtures.plugins[0];
      if (!entry) throw new Error("no plugin fixture");
      const installed: Plugin = {
        ...entry,
        id: `plg_01JD7ZK3M0000000000000009${String(this.calls.length % 10)}`,
        name: ref.split("/").pop() ?? ref,
        installed_by: "human:ada",
        enabled: true,
      };
      if (!(this.plugins instanceof ApiError)) this.plugins = [...this.plugins, installed];
      if (!(this.catalog instanceof ApiError)) {
        this.catalog = this.catalog.map((e) => (e.ref === ref ? { ...e, installed: true } : e));
      }
      return installed;
    });
  uninstallPlugin = (id: string) =>
    this.change(`uninstallPlugin ${id}`, () => {
      this.findPlugin(id);
      if (!(this.plugins instanceof ApiError))
        this.plugins = this.plugins.filter((p) => p.id !== id);
    });
  enablePlugin = (id: string) => this.change(`enablePlugin ${id}`, () => this.setEnabled(id, true));
  disablePlugin = (id: string) =>
    this.change(`disablePlugin ${id}`, () => this.setEnabled(id, false));
  listHumans = () => this.settle(this.humans);
  createHuman = (body: { name: string; display_name: string; password: string }) =>
    this.change(`createHuman ${body.name}`, () => {
      const created: Human = {
        name: body.name,
        display_name: body.display_name,
        uid: 1002,
        created_at: "2026-11-19T20:00:00Z",
        last_login_at: null,
      };
      if (!(this.humans instanceof ApiError)) this.humans = [...this.humans, created];
      return created;
    });
  /** The signed-in human's present password, for the own-password-change check. */
  currentPassword = "old-password-12345";

  updateHuman = (
    name: string,
    body: { display_name?: string; password?: string; current_password?: string },
  ) =>
    this.change(`updateHuman ${name} ${Object.keys(body).join(",")}`, (): Human => {
      if (body.password !== undefined && body.current_password !== this.currentPassword) {
        throw new ApiError(400, {
          type: "urn:agentsone:problem:validation",
          title: "The request is not valid",
          status: 400,
          errors: [{ pointer: "/current_password", message: "Wrong password." }],
        });
      }
      const list = this.humans instanceof ApiError ? fixtures.humans : this.humans;
      const base = list.find((h) => h.name === name) ?? fixtures.humans[0];
      if (!base) throw new Error("no human fixture");
      const next = { ...base, name, display_name: body.display_name ?? base.display_name };
      if (!(this.humans instanceof ApiError)) {
        this.humans = this.humans.map((h) => (h.name === name ? next : h));
      }
      return next;
    });
  deleteHuman = (name: string) =>
    this.change(`deleteHuman ${name}`, () => {
      if (!(this.humans instanceof ApiError))
        this.humans = this.humans.filter((h) => h.name !== name);
    });
  listThemes = () => Promise.resolve(this.themes);

  // machine
  processes: MachineProcess[] | ApiError = [...fixtures.processes];
  services: MachineService[] | ApiError = [...fixtures.services];
  packages: MachinePackage[] | ApiError = [...fixtures.packages];
  machineUsers: MachineUser[] | ApiError = [...fixtures.machineUsers];
  files: FileList | ApiError = fixtures.files;
  file: FileContent | ApiError = fixtures.file;
  ptys: PtySession[] | ApiError = [...fixtures.ptys];
  listProcesses = (cursor?: string) =>
    this.settle(this.processes).then((all) => paged(all, cursor, this.pageSize));
  listServices = (cursor?: string) =>
    this.settle(this.services).then((all) => paged(all, cursor, this.pageSize));
  listPackages = ({ q, cursor }: { q?: string; cursor?: string }) => {
    this.calls.push(`listPackages ${q ?? ""}`.trimEnd());
    return this.settle(this.packages).then((all) =>
      paged(
        all.filter((p) => q === undefined || p.name.includes(q)),
        cursor,
        this.pageSize,
      ),
    );
  };
  listMachineUsers = (cursor?: string) =>
    this.settle(this.machineUsers).then((all) => paged(all, cursor, this.pageSize));
  listFiles = (path: string) => {
    this.calls.push(`listFiles ${path}`);
    return this.settle(this.files);
  };
  readFile = (path: string) => {
    this.calls.push(`readFile ${path}`);
    return this.settle(this.file);
  };
  listPtySessions = (cursor?: string) =>
    this.settle(this.ptys).then((all) => paged(all, cursor, this.pageSize));
  createPtySession = () =>
    this.change("createPtySession", () => {
      if (!(this.ptys instanceof ApiError)) this.ptys = [fixtures.newPty, ...this.ptys];
      return fixtures.newPty;
    });
  closePtySession = (id: string) =>
    this.change(`closePtySession ${id}`, () => {
      if (!(this.ptys instanceof ApiError)) this.ptys = this.ptys.filter((p) => p.id !== id);
    });
}

function meHuman() {
  const principal = fixtures.me;
  if (principal.kind !== "human") throw new Error("fixture getMe is not a human");
  return principal.human;
}

/** Render the whole app at a path, with a fake daemon. */
export function renderApp(path: string, api = new FakeApi()) {
  const view = render(
    <AppProviders api={api}>
      <MemoryRouter initialEntries={[path]}>
        <AppRoutes />
      </MemoryRouter>
    </AppProviders>,
  );
  return { ...view, api };
}

/** Render one piece of the app with providers and a fake daemon. */
export function renderWith(node: ReactNode, api = new FakeApi(), path = "/") {
  const view = render(
    <AppProviders api={api}>
      <MemoryRouter initialEntries={[path]}>{node}</MemoryRouter>
    </AppProviders>,
  );
  return { ...view, api };
}
