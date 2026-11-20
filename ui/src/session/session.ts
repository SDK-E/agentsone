// SPDX-License-Identifier: MIT
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { ApiError } from "../api/api";
import { useApi } from "../app/api-context";

export const meKey = ["me"] as const;
export const setupKey = ["setup"] as const;

/** How long a refresh that failed waits before it asks again, while the last good answer stays. */
export const RETRY_AFTER_MS = 10_000;

/** Ask again on its own only when a refresh failed and there is an answer to keep showing. */
const retryWhileStale = (query: { state: { status: string; data: unknown } }) =>
  query.state.status === "error" && query.state.data !== undefined ? RETRY_AFTER_MS : false;

/** The setup phase, readable by anyone on loopback before sign-in. */
export function useSetupStatus() {
  const api = useApi();
  return useQuery({
    queryKey: setupKey,
    queryFn: () => api.getSetupStatus(),
    retry: false,
    refetchInterval: retryWhileStale,
  });
}

/** The signed-in caller; a 401 means nobody is signed in. */
export function useMe() {
  const api = useApi();
  return useQuery({
    queryKey: meKey,
    queryFn: () => api.getMe(),
    retry: false,
    refetchInterval: retryWhileStale,
  });
}

export function isSignedOut(error: unknown): boolean {
  return error instanceof ApiError && error.status === 401;
}

export function useSignIn() {
  const api = useApi();
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ name, password }: { name: string; password: string }) =>
      api.createSession(name, password),
    onSuccess: () => client.invalidateQueries({ queryKey: meKey }),
  });
}

export function useSignOut() {
  const api = useApi();
  const client = useQueryClient();
  return useMutation({
    mutationFn: () => api.deleteSession(),
    onSettled: () => {
      client.clear();
    },
  });
}
