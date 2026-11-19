// SPDX-License-Identifier: MIT
import { readFile } from "node:fs/promises";

import { describe, expect, it } from "vitest";

import { createAgentsOneClient, DEFAULT_BASE_URL, isProblem } from "../src/index.js";

const fixtureUrl = new URL("../../../contract/fixtures/api/listAgents.json", import.meta.url);

interface Fixture {
  status: number;
  body: unknown;
}

async function loadFixture(): Promise<Fixture> {
  return JSON.parse(await readFile(fixtureUrl, "utf8")) as Fixture;
}

function stubFetch(status: number, body: unknown, contentType: string, seen: Request[] = []) {
  const stub: typeof fetch = (input, init) => {
    seen.push(input instanceof Request ? input : new Request(input, init));
    return Promise.resolve(
      new Response(JSON.stringify(body), { status, headers: { "content-type": contentType } }),
    );
  };
  return stub;
}

describe("createAgentsOneClient", () => {
  it("defaults to the daemon's loopback API base", async () => {
    const fixture = await loadFixture();
    const seen: Request[] = [];
    const client = createAgentsOneClient({
      fetch: stubFetch(fixture.status, fixture.body, "application/json", seen),
    });

    await client.GET("/agents");

    expect(DEFAULT_BASE_URL).toBe("http://127.0.0.1:7700/v1");
    expect(seen[0]?.url).toBe("http://127.0.0.1:7700/v1/agents");
  });

  it("sends typed requests and returns typed data from the golden fixture", async () => {
    const fixture = await loadFixture();
    const seen: Request[] = [];
    const client = createAgentsOneClient({
      baseUrl: "http://127.0.0.1:5173/v1",
      fetch: stubFetch(fixture.status, fixture.body, "application/json", seen),
      headers: { "x-test": "1" },
    });

    const { data, error } = await client.GET("/agents", { params: { query: { limit: 10 } } });

    expect(error).toBeUndefined();
    expect(data?.items[0]?.name).toBe("founder");
    expect(seen).toHaveLength(1);
    expect(seen[0]?.method).toBe("GET");
    expect(seen[0]?.url).toBe("http://127.0.0.1:5173/v1/agents?limit=10");
    expect(seen[0]?.headers.get("x-test")).toBe("1");
  });

  it("returns the problem document as the error on failure", async () => {
    const problem = {
      type: "urn:agentsone:problem:unauthenticated",
      title: "Not signed in",
      status: 401,
    };
    const client = createAgentsOneClient({
      fetch: stubFetch(401, problem, "application/problem+json"),
    });

    const { data, error } = await client.GET("/agents");

    expect(data).toBeUndefined();
    expect(isProblem(error)).toBe(true);
  });
});

describe("isProblem", () => {
  it("rejects values that are not problem documents", () => {
    expect(isProblem(null)).toBe(false);
    expect(isProblem("x")).toBe(false);
    expect(isProblem({ title: "t" })).toBe(false);
    expect(isProblem({ type: "urn:x", title: "t", status: 400 })).toBe(true);
  });
});
