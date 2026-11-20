// SPDX-License-Identifier: MIT
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen } from "@testing-library/react";
import { I18nextProvider } from "react-i18next";
import { MemoryRouter } from "react-router";
import { describe, expect, it } from "vitest";

import { ApiError } from "../api/api";
import { ApiProvider } from "../app/api-context";
import { i18n } from "../i18n/i18n";
import { meKey, setupKey } from "../session/session";
import { FakeApi } from "../test/fake-api";
import { ThemeProvider } from "../theme/theme-provider";
import { AppShell } from "./app-shell";

function renderShell(api: FakeApi) {
  const client = new QueryClient({ defaultOptions: { queries: { staleTime: 5_000 } } });
  render(
    <I18nextProvider i18n={i18n}>
      <ApiProvider api={api}>
        <QueryClientProvider client={client}>
          <ThemeProvider>
            <MemoryRouter>
              <AppShell>
                <p>The page</p>
              </AppShell>
            </MemoryRouter>
          </ThemeProvider>
        </QueryClientProvider>
      </ApiProvider>
    </I18nextProvider>,
  );
  return client;
}

const down = () => new ApiError(503, { type: "about:blank", title: "Daemon is down", status: 503 });

describe("AppShell", () => {
  it("keeps the page in place when a refresh in the background fails", async () => {
    const api = new FakeApi();
    const client = renderShell(api);
    expect(await screen.findByText("The page")).toBeVisible();

    api.me = down();
    await act(async () => {
      await client.refetchQueries({ queryKey: meKey });
      await client.refetchQueries({ queryKey: setupKey });
    });

    expect(client.getQueryState(meKey)?.status).toBe("error");
    expect(client.getQueryData(meKey)).toBeDefined();
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    expect(screen.getByText("The page")).toBeVisible();
    expect(screen.queryByRole("heading", { name: "Agents One is not answering" })).toBeNull();
  });

  it("shows the error when the first load fails", async () => {
    const api = new FakeApi();
    api.me = down();
    renderShell(api);

    expect(await screen.findByRole("alert")).toBeVisible();
    expect(screen.queryByText("The page")).toBeNull();
  });
});
