// SPDX-License-Identifier: MIT
import { describe, expect, it } from "vitest";

import { createAgentsOneClient, isProblem } from "../src/index.js";

describe("createAgentsOneClient", () => {
  it("sends typed requests to the base URL and returns typed data", async () => {
    const seen: Request[] = [];
    const stub: typeof fetch = (input) => {
      seen.push(input instanceof Request ? input : new Request(input));
      return Promise.resolve(
        new Response(JSON.stringify({ items: [{ name: "founder", kind: "root", line: "x" }] }), {
          status: 200,
          headers: { "content-type": "application/json" },
        }),
      );
    };

    const client = createAgentsOneClient({
      baseUrl: "http://127.0.0.1:7700",
      fetch: stub,
      headers: { "x-test": "1" },
    });
    const { data, error } = await client.GET("/v1/agents");

    expect(error).toBeUndefined();
    expect(data?.items[0]?.name).toBe("founder");
    expect(seen).toHaveLength(1);
    expect(seen[0]?.method).toBe("GET");
    expect(seen[0]?.url).toBe("http://127.0.0.1:7700/v1/agents");
    expect(seen[0]?.headers.get("x-test")).toBe("1");
  });

  it("returns the problem document as the error on failure", async () => {
    const problem = { type: "about:blank", title: "Not Implemented", status: 501 };
    const stub: typeof fetch = () =>
      Promise.resolve(
        new Response(JSON.stringify(problem), {
          status: 501,
          headers: { "content-type": "application/problem+json" },
        }),
      );

    const client = createAgentsOneClient({ baseUrl: "http://127.0.0.1:7700", fetch: stub });
    const { data, error } = await client.GET("/v1/agents");

    expect(data).toBeUndefined();
    expect(isProblem(error)).toBe(true);
  });
});

describe("isProblem", () => {
  it("rejects values that are not problem documents", () => {
    expect(isProblem(null)).toBe(false);
    expect(isProblem("x")).toBe(false);
    expect(isProblem({ title: "t" })).toBe(false);
    expect(isProblem({ title: "t", status: 400 })).toBe(true);
  });
});
