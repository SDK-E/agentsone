// SPDX-License-Identifier: MIT
import { describe, expect, it, vi } from "vitest";

import { fixtures } from "../test/fake-api";
import { ApiError, createHttpApi, type LiveStreamFrame } from "./api";

const BASE = "http://agentsone.test/v1";

/** A stand-in for the network that records each request and answers from a table. */
function network(answer: (method: string, path: string) => Response) {
  const seen: string[] = [];
  const fetch = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const request = input instanceof Request ? input : new Request(input, init);
    const path = new URL(request.url).pathname.replace(/^\/v1/, "");
    seen.push(`${request.method} ${path}`);
    return Promise.resolve(answer(request.method, path));
  });
  return { fetch: fetch as unknown as typeof globalThis.fetch, seen };
}

const json = (status: number, body: unknown, type = "application/json") =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": type } });

describe("createHttpApi", () => {
  it("calls each contract path and returns the body", async () => {
    const { fetch, seen } = network((method, path) => {
      if (path === "/router/live") return json(200, { items: fixtures.live, next_cursor: null });
      if (path === "/router/route") return json(201, fixtures.route);
      if (path === "/session") {
        return method === "DELETE"
          ? new Response(null, { status: 204 })
          : json(201, { human: { name: "ada" }, expires_at: "2026-11-20T06:15:00Z" });
      }
      if (path === "/chart") return json(200, { roots: fixtures.chart });
      if (path.endsWith("/chat")) {
        return method === "GET"
          ? json(200, { items: fixtures.chat, next_cursor: null })
          : json(201, fixtures.chat[0]);
      }
      if (path === "/setup") return json(200, fixtures.setup);
      if (path === "/session/me") return json(200, fixtures.me);
      if (path === "/setup/engine-licence") {
        return method === "GET" ? json(200, fixtures.licence) : json(202, fixtures.engineFetch);
      }
      if (path === "/setup/engine-fetch") return json(200, fixtures.engineFetch);
      if (path === "/setup/first-human") return json(201, { name: "ada" });
      if (path === "/settings/theme") return json(200, { theme_id: "thm_1", mode: "dark" });
      if (path.startsWith("/themes/")) return json(200, fixtures.theme);
      return json(404, { title: "Not found", status: 404 });
    });
    const api = createHttpApi({ baseUrl: BASE, fetch });

    expect(await api.listLiveList()).toHaveLength(fixtures.live.length);
    expect((await api.routeRequest("hello")).chosen).toBe("builder");
    expect((await api.createSession("ada", "pw")).name).toBe("ada");
    await api.deleteSession();
    expect(await api.getChart()).toEqual(fixtures.chart);
    expect(await api.listChatMessages("builder")).toHaveLength(3);
    expect((await api.sendChatMessage("builder", "hi")).id).toBe(fixtures.chat[0]?.id);
    expect((await api.getSetupStatus()).phase).toBe("ready");
    expect((await api.getMe()).kind).toBe("human");
    expect((await api.getEngineLicence()).engine).toBe("claude-code");
    expect((await api.acceptEngineLicence("v1")).state).toBe("downloading");
    expect((await api.getEngineFetchStatus()).progress).toBe(0.42);
    expect(
      (await api.createFirstHuman({ name: "ada", display_name: "Ada", password: "x" })).name,
    ).toBe("ada");
    expect((await api.getThemeSelection()).mode).toBe("dark");
    expect((await api.setThemeSelection({ mode: "dark" })).mode).toBe("dark");
    expect((await api.getTheme("thm_1")).builtin).toBe(true);
    expect(seen).toContain("POST /router/route");
    expect(seen).toContain("GET /agents/builder/chat");
    expect(seen).toContain("DELETE /session");
  });

  it("turns a problem document into an ApiError with the daemon's words", async () => {
    const { fetch } = network(() =>
      json(503, fixtures.routeError.problem, "application/problem+json"),
    );
    const api = createHttpApi({ baseUrl: BASE, fetch });

    const error = await api.routeRequest("hi").catch((e: unknown) => e);

    expect(error).toBeInstanceOf(ApiError);
    expect((error as ApiError).status).toBe(503);
    expect((error as ApiError).message).toContain("The router agent is restarting");
  });

  it("reads stream frames and reports when the stream ends", async () => {
    const frame: LiveStreamFrame = { kind: "removed", agent: "keeper" };
    const { fetch } = network(
      () =>
        new Response(`event: removed\ndata: ${JSON.stringify(frame)}\n\n`, {
          status: 200,
          headers: { "Content-Type": "text/event-stream" },
        }),
    );
    const api = createHttpApi({ baseUrl: BASE, fetch });
    const frames: LiveStreamFrame[] = [];

    await new Promise<void>((resolve) => {
      api.streamLiveList({
        onFrame: (f) => frames.push(f),
        onClose: resolve,
      });
    });

    expect(frames).toEqual([frame]);
  });

  it("reports a refused stream as closed so the caller can poll", async () => {
    const { fetch, seen } = network(() => json(501, { title: "Not implemented", status: 501 }));
    const api = createHttpApi({ baseUrl: BASE, fetch });

    await new Promise<void>((resolve) => {
      api.streamChat("builder", { onFrame: vi.fn(), onClose: resolve });
    });
    await new Promise<void>((resolve) => {
      api.streamAgentOutput("builder", { onFrame: vi.fn(), onClose: resolve });
    });

    expect(seen).toEqual(["GET /agents/builder/chat/stream", "GET /agents/builder/output"]);
  });

  it("does not report a stream the caller closed itself", async () => {
    const { fetch } = network(
      () =>
        new Response(new ReadableStream(), { headers: { "Content-Type": "text/event-stream" } }),
    );
    const api = createHttpApi({ baseUrl: BASE, fetch });
    const onClose = vi.fn();

    const stop = api.streamLiveList({ onFrame: vi.fn(), onClose });
    stop();
    await Promise.resolve();

    expect(onClose).not.toHaveBeenCalled();
  });
});
