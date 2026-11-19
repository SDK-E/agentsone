// SPDX-License-Identifier: MIT
import { createContext, useContext, type ReactNode } from "react";

import type { Api } from "../api/api";

const ApiContext = createContext<Api | null>(null);

export function ApiProvider({ api, children }: { api: Api; children: ReactNode }) {
  return <ApiContext value={api}>{children}</ApiContext>;
}

/** The daemon API for the current tree. */
export function useApi(): Api {
  const api = useContext(ApiContext);
  if (!api) throw new Error("useApi needs an ApiProvider above it");
  return api;
}
