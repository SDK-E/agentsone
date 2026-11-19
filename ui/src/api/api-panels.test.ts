// SPDX-License-Identifier: MIT
import { describe, expect, it, vi } from "vitest";

import { fixtures } from "../test/fake-api";
import { ApiError, createHttpApi, type TaskStreamFrame } from "./api";

const BASE = "http://agentsone.test/v1";

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

/** A stand-in for the network that records each request line, with its query and body. */
function network(answer: (method: string, path: string) => Response) {
  const seen: string[] = [];
  const bodies: unknown[] = [];
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const request = input instanceof Request ? input : new Request(input, init);
    const url = new URL(request.url);
    seen.push(`${request.method} ${url.pathname.replace(/^\/v1/, "")}${url.search}`);
    if (request.method === "POST") bodies.push(await request.clone().json());
    return answer(request.method, url.pathname.replace(/^\/v1/, ""));
  });
  return { fetch: fetch as unknown as typeof globalThis.fetch, seen, bodies };
}

describe("createHttpApi for monitor, tasks and audit", () => {
  it("reads budget, metrics, schedules and the audit log", async () => {
    const { fetch, seen } = network((_method, path) => {
      if (path === "/budget") return json(200, fixtures.budget);
      if (path === "/budget/split") return json(200, fixtures.split);
      if (path === "/machine/metrics") return json(200, fixtures.metrics);
      if (path === "/schedules") return json(200, { items: fixtures.schedules, next_cursor: null });
      if (path === "/audit") return json(200, { items: fixtures.audit, next_cursor: "c2" });
      if (path === "/audit/verify") return json(200, fixtures.verification);
      return json(404, { title: "Not found", status: 404 });
    });
    const api = createHttpApi({ baseUrl: BASE, fetch });

    expect((await api.getBudget()).wall).toBe("hard");
    expect((await api.getBudgetSplit()).nodes[0]?.wall).toBe("soft");
    expect((await api.getMachineMetrics()).cpu.cores).toBe(4);
    expect(await api.listSchedules()).toHaveLength(2);
    expect((await api.listAuditRecords("c1")).next_cursor).toBe("c2");
    expect((await api.listAuditRecords()).items).toHaveLength(fixtures.audit.length);
    expect((await api.verifyAuditChain()).ok).toBe(true);
    expect(seen).toContain("GET /audit?cursor=c1");
    expect(seen).toContain("GET /audit");
    expect(seen).toContain("POST /audit/verify");
  });

  it("lists tasks with only the filters that are set", async () => {
    const { fetch, seen } = network(() => json(200, { items: fixtures.tasks, next_cursor: null }));
    const api = createHttpApi({ baseUrl: BASE, fetch });

    await api.listTasks({});
    await api.listTasks({ status: "done", assignee: "keeper" }, "next-1");

    expect(seen[0]).toBe("GET /tasks");
    expect(seen[1]).toContain("status=done");
    expect(seen[1]).toContain("assignee=keeper");
    expect(seen[1]).toContain("cursor=next-1");
  });

  it("gets and creates a task", async () => {
    const { fetch, seen, bodies } = network((method) =>
      method === "POST" ? json(201, fixtures.newTask) : json(200, fixtures.tasks[0]),
    );
    const api = createHttpApi({ baseUrl: BASE, fetch });

    expect((await api.getTask("tsk_01JD7ZK3M00000000000000001")).title).toBe("Budget walls page");
    expect((await api.createTask({ title: "Link the page", assignee: "builder" })).id).toBe(
      fixtures.newTask.id,
    );
    expect(seen).toContain("GET /tasks/tsk_01JD7ZK3M00000000000000001");
    expect(bodies).toEqual([{ title: "Link the page", assignee: "builder" }]);
  });

  it("raises the daemon's own words when assigning is forbidden", async () => {
    const { fetch } = network(() =>
      json(403, { type: "about:blank", title: "Forbidden", status: 403, detail: "Not below you." }),
    );
    const api = createHttpApi({ baseUrl: BASE, fetch });

    const failure = await api
      .createTask({ title: "x", assignee: "keeper" })
      .catch((e: unknown) => e);

    expect(failure).toBeInstanceOf(ApiError);
    expect((failure as ApiError).status).toBe(403);
    expect((failure as ApiError).message).toBe("Not below you.");
  });

  it("follows the task stream and stops when asked", async () => {
    const frame: TaskStreamFrame = { kind: "task", task: fixtures.newTask };
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(new TextEncoder().encode(`data: ${JSON.stringify(frame)}\n\n`));
        controller.close();
      },
    });
    const { fetch } = network(
      () => new Response(body, { headers: { "Content-Type": "text/event-stream" } }),
    );
    const api = createHttpApi({ baseUrl: BASE, fetch });
    const frames: TaskStreamFrame[] = [];

    await new Promise<void>((done) => {
      const stop = api.streamTasks({
        onFrame: (f) => frames.push(f),
        onClose: () => {
          stop();
          done();
        },
      });
    });

    expect(frames).toEqual([frame]);
  });
});
