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
// juno: monitor/tasks/audit
import auditList from "../../../contract/fixtures/api/listAuditRecords.json";
import budget from "../../../contract/fixtures/api/getBudget.json";
import budgetSplit from "../../../contract/fixtures/api/getBudgetSplit.json";
import createdTask from "../../../contract/fixtures/api/createTask.json";
import machineMetrics from "../../../contract/fixtures/api/getMachineMetrics.json";
import schedulesList from "../../../contract/fixtures/api/listSchedules.json";
import tasksList from "../../../contract/fixtures/api/listTasks.json";
import verification from "../../../contract/fixtures/api/verifyAuditChain.json";
import theme from "../../../contract/fixtures/api/getTheme.json";
import chat from "../../../contract/fixtures/api/listChatMessages.json";
import live from "../../../contract/fixtures/api/listLiveList.json";
import route from "../../../contract/fixtures/api/routeRequest.json";
import routeError from "../../../contract/fixtures/api/routeRequest.error.json";
import {
  ApiError,
  type AgentOutputFrame,
  type Api,
  type Assignment,
  type AuditQuery,
  type AuditRecord,
  type AuditVerification,
  type Budget,
  type BudgetSplit,
  type MachineMetrics,
  type Page,
  type Schedule,
  type Task,
  type TaskCreate,
  type TaskFilter,
  type TaskStreamFrame,
  type ChartNode,
  type ChatMessage,
  type ChatStreamFrame,
  type EngineFetchStatus,
  type EngineLicence,
  type Freeze,
  type InstalledTheme,
  type LiveEntry,
  type LiveStreamFrame,
  type Principal,
  type SetupStatus,
  type StreamHandlers,
} from "../api/api";
import { AppProviders, AppRoutes } from "../app/app";

export const fixtures = {
  setup: setup.body as SetupStatus,
  me: me.body as Principal,
  live: live.body.items as LiveEntry[],
  chart: chart.body.roots as ChartNode[],
  chat: chat.body.items as ChatMessage[],
  route: route.body as Assignment,
  routeError: new ApiError(503, routeError.body),
  licence: licence.body as EngineLicence,
  engineFetch: engineFetch.body as EngineFetchStatus,
  theme: theme.body as InstalledTheme,
  // juno: monitor/tasks/audit
  budget: budget.body as Budget,
  split: budgetSplit.body as BudgetSplit,
  metrics: machineMetrics.body satisfies MachineMetrics,
  tasks: tasksList.body.items as Task[],
  newTask: createdTask.body as Task,
  schedules: schedulesList.body.items as Schedule[],
  // The generated type of `details` is `Record<string, never>`; the fixtures carry real values.
  audit: auditList.body.items as unknown as AuditRecord[],
  verification: verification.body as AuditVerification,
};

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
  getEngineLicence = () => Promise.resolve({ ...fixtures.licence, accepted: false });
  acceptEngineLicence = (version: string) => {
    this.calls.push(`acceptEngineLicence ${version}`);
    this.setup = { ...this.setup, phase: "fetching-engine", engine: fixtures.engineFetch };
    return Promise.resolve(fixtures.engineFetch);
  };
  getEngineFetchStatus = () => Promise.resolve(fixtures.engineFetch);
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
  getThemeSelection = () =>
    Promise.resolve({ theme_id: fixtures.theme.id, mode: "system" as const });
  setThemeSelection = (update: { mode?: "system" | "light" | "dark" }) => {
    this.calls.push(`setThemeSelection ${update.mode ?? ""}`);
    return Promise.resolve({ theme_id: fixtures.theme.id, mode: update.mode ?? "system" });
  };
  getTheme = () => Promise.resolve(fixtures.theme);
  freezes: Freeze[] = [];
  listFreezes = () => Promise.resolve(this.freezes);

  // juno: monitor/tasks/audit
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
