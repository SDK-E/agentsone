// SPDX-License-Identifier: MIT
//
// The Memory, Plugins and Settings parts of the HTTP adapter: right paths, right query, right body.
import { describe, expect, it, vi } from "vitest";

import { fixtures } from "../test/fake-api";
import { ApiError, createHttpApi } from "./api";

const BASE = "http://agentsone.test/v1";

interface Seen {
  method: string;
  path: string;
  query: string;
  body: unknown;
}

function network(answer: (seen: Seen) => Response) {
  const seen: Seen[] = [];
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const request = input instanceof Request ? input : new Request(input, init);
    const url = new URL(request.url);
    const text = await request.text();
    const entry: Seen = {
      method: request.method,
      path: url.pathname.replace(/^\/v1/, ""),
      query: url.search,
      body: text === "" ? undefined : (JSON.parse(text) as unknown),
    };
    seen.push(entry);
    return answer(entry);
  });
  return { fetch: fetch as unknown as typeof globalThis.fetch, seen };
}

const json = (status: number, body: unknown, type = "application/json") =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": type } });
const empty = () => new Response(null, { status: 204 });

describe("memory calls", () => {
  it("lists a page, filtered by scope and continued by cursor", async () => {
    const { fetch, seen } = network(() =>
      json(200, { items: fixtures.memory, next_cursor: "page2" }),
    );
    const api = createHttpApi({ baseUrl: BASE, fetch });

    const page = await api.listMemory({ scope: "team", cursor: "abc" });

    expect(page.items).toHaveLength(fixtures.memory.length);
    expect(page.next_cursor).toBe("page2");
    expect(seen[0]?.path).toBe("/memory");
    expect(seen[0]?.query).toContain("scope=team");
    expect(seen[0]?.query).toContain("cursor=abc");
  });

  it("searches with the words and the scope", async () => {
    const { fetch, seen } = network(() =>
      json(200, { items: fixtures.memoryHits, next_cursor: null }),
    );
    const api = createHttpApi({ baseUrl: BASE, fetch });

    const page = await api.searchMemory({ q: "disk", scope: "common" });

    expect(page.items[0]?.snippet).toContain("[");
    expect(seen[0]?.path).toBe("/memory/search");
    expect(seen[0]?.query).toContain("q=disk");
    expect(seen[0]?.query).toContain("scope=common");
  });

  it("deletes one record", async () => {
    const { fetch, seen } = network(empty);
    const api = createHttpApi({ baseUrl: BASE, fetch });

    await api.deleteMemory("mem_01JD7ZK3M00000000000000001");

    expect(seen[0]).toMatchObject({
      method: "DELETE",
      path: "/memory/mem_01JD7ZK3M00000000000000001",
    });
  });
});

describe("plugin calls", () => {
  it("lists installed plugins, optionally by kind", async () => {
    const { fetch, seen } = network(() =>
      json(200, { items: fixtures.plugins, next_cursor: null }),
    );
    const api = createHttpApi({ baseUrl: BASE, fetch });

    const page = await api.listPlugins({ kind: "skill" });

    expect(page.items).toHaveLength(fixtures.plugins.length);
    expect(seen[0]?.path).toBe("/plugins");
    expect(seen[0]?.query).toContain("kind=skill");
  });

  it("lists the catalog sources", async () => {
    const { fetch } = network(() =>
      json(200, { items: fixtures.pluginSources, next_cursor: null }),
    );
    const api = createHttpApi({ baseUrl: BASE, fetch });

    expect(await api.listPluginSources()).toHaveLength(4);
  });

  it("searches the catalog with words, source and kind", async () => {
    const { fetch, seen } = network(() =>
      json(200, { items: fixtures.catalog, next_cursor: null }),
    );
    const api = createHttpApi({ baseUrl: BASE, fetch });

    await api.searchPluginCatalog({ q: "pdf", source: "agent-skills", kind: "skill" });

    expect(seen[0]?.path).toBe("/plugin-catalog");
    expect(seen[0]?.query).toContain("q=pdf");
    expect(seen[0]?.query).toContain("source=agent-skills");
    expect(seen[0]?.query).toContain("kind=skill");
  });

  it("installs from a source and a reference", async () => {
    const { fetch, seen } = network(() => json(201, fixtures.plugins[0]));
    const api = createHttpApi({ baseUrl: BASE, fetch });

    await api.installPlugin({
      source: "agentsone",
      ref: "SDK-E/agentsone/plugins/disk-watch-panel",
    });

    expect(seen[0]).toMatchObject({
      method: "POST",
      path: "/plugins",
      body: { source: "agentsone", ref: "SDK-E/agentsone/plugins/disk-watch-panel" },
    });
  });

  it("turns a plugin on and off, and removes it", async () => {
    const { fetch, seen } = network(({ method }) =>
      method === "DELETE" ? empty() : json(200, fixtures.plugins[0]),
    );
    const api = createHttpApi({ baseUrl: BASE, fetch });

    await api.enablePlugin("plg_1");
    await api.disablePlugin("plg_1");
    await api.uninstallPlugin("plg_1");

    expect(seen.map((s) => `${s.method} ${s.path}`)).toEqual([
      "POST /plugins/plg_1/enable",
      "POST /plugins/plg_1/disable",
      "DELETE /plugins/plg_1",
    ]);
  });

  it("keeps the daemon's words when an install fails", async () => {
    const { fetch } = network(() =>
      json(
        409,
        { type: "about:blank", title: "Conflict", status: 409, detail: "Already installed." },
        "application/problem+json",
      ),
    );
    const api = createHttpApi({ baseUrl: BASE, fetch });

    await expect(api.installPlugin({ source: "agentsone", ref: "x" })).rejects.toThrow(ApiError);
    await expect(api.installPlugin({ source: "agentsone", ref: "x" })).rejects.toMatchObject({
      message: "Already installed.",
    });
  });
});

describe("settings calls", () => {
  it("lists, adds, renames and removes people", async () => {
    const { fetch, seen } = network(({ method, path }) => {
      if (method === "DELETE") return empty();
      if (method === "GET") return json(200, { items: fixtures.humans, next_cursor: null });
      return json(path === "/humans" ? 201 : 200, fixtures.humans[0]);
    });
    const api = createHttpApi({ baseUrl: BASE, fetch });

    expect(await api.listHumans()).toHaveLength(2);
    await api.createHuman({ name: "kit", display_name: "Kit", password: "test-only-value-1" });
    await api.updateHuman("ada", { password: "test-only-value-2" });
    await api.deleteHuman("grace");

    expect(seen.map((s) => `${s.method} ${s.path}`)).toEqual([
      "GET /humans",
      "POST /humans",
      "PATCH /humans/ada",
      "DELETE /humans/grace",
    ]);
    expect(seen[2]?.body).toEqual({ password: "test-only-value-2" });
  });

  it("sends the current password with an own password change", async () => {
    const { fetch, seen } = network(() => json(200, fixtures.humans[0]));
    const api = createHttpApi({ baseUrl: BASE, fetch });

    await api.updateHuman("ada", {
      current_password: "test-only-value-1",
      password: "test-only-value-2",
    });

    expect(seen[0]?.body).toEqual({
      current_password: "test-only-value-1",
      password: "test-only-value-2",
    });
  });

  it("keeps how long to wait when the sign-in throttle answers 429", async () => {
    const { fetch } = network(
      () =>
        new Response(
          JSON.stringify({
            type: "urn:agentsone:problem:rate-limited",
            title: "Too many requests",
            status: 429,
          }),
          {
            status: 429,
            headers: { "Content-Type": "application/problem+json", "Retry-After": "30" },
          },
        ),
    );
    const api = createHttpApi({ baseUrl: BASE, fetch });

    const failure: unknown = await api
      .updateHuman("ada", { current_password: "x", password: "test-only-value-2" })
      .catch((error: unknown) => error);

    expect(failure).toBeInstanceOf(ApiError);
    expect((failure as ApiError).status).toBe(429);
    expect((failure as ApiError).retryAfter).toBe(30);
  });

  it("lists installed themes", async () => {
    const { fetch, seen } = network(() =>
      json(200, { items: [fixtures.theme], next_cursor: null }),
    );
    const api = createHttpApi({ baseUrl: BASE, fetch });

    expect(await api.listThemes()).toHaveLength(1);
    expect(seen[0]?.path).toBe("/themes");
  });
});
