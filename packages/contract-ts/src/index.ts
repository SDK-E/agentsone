// SPDX-License-Identifier: MIT
//
// @agentsone/contract: TypeScript types generated from the Agents One contract, and a thin
// typed client for the daemon API. The types under ./generated come from `pnpm run gen`.
import createClient, { type Client } from "openapi-fetch";

import type { components, paths } from "./generated/openapi.js";

export type { components, operations, paths } from "./generated/openapi.js";
export type * from "./generated/schemas/index.js";

/** An RFC 9457 problem document, as returned by every failing API call. */
export type Problem = components["schemas"]["Problem"];

/** The typed API client returned by {@link createAgentsOneClient}. */
export type AgentsOneClient = Client<paths>;

/**
 * The daemon's loopback API base inside the VM. Paths in the contract are relative to it
 * (for example "/agents"), so a custom base URL must also end in "/v1".
 */
export const DEFAULT_BASE_URL = "http://127.0.0.1:7700/v1";

export interface AgentsOneClientOptions {
  /** Base URL of the API, ending in "/v1". Defaults to {@link DEFAULT_BASE_URL}. */
  baseUrl?: string;
  /** Fetch implementation to use. Defaults to the global fetch. */
  fetch?: typeof fetch;
  /** Headers sent with every request. */
  headers?: HeadersInit;
}

/** Create a typed client for the Agents One daemon API. */
export function createAgentsOneClient(options: AgentsOneClientOptions = {}): AgentsOneClient {
  return createClient<paths>({
    baseUrl: options.baseUrl ?? DEFAULT_BASE_URL,
    ...(options.fetch !== undefined && { fetch: options.fetch }),
    ...(options.headers !== undefined && { headers: options.headers }),
  });
}

/** Report whether a value looks like an RFC 9457 problem document. */
export function isProblem(value: unknown): value is Problem {
  if (typeof value !== "object" || value === null) return false;
  const v = value as Record<string, unknown>;
  return typeof v.title === "string" && typeof v.status === "number";
}
