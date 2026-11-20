// SPDX-License-Identifier: MIT
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
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

/** Ask the machine again for who is signed in, and let the screen settle. */
async function refresh(client: QueryClient) {
  await act(async () => {
    await client.refetchQueries({ queryKey: meKey });
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
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

  it("says so quietly while a refresh keeps failing, and clears the line on the next good one", async () => {
    const api = new FakeApi();
    const good = api.me;
    const client = renderShell(api);
    expect(await screen.findByText("The page")).toBeVisible();
    // The place for the line is there from the start, so a screen reader hears it appear.
    const line = screen.getByRole("status");
    expect(line).toHaveAttribute("aria-live", "polite");
    expect(line).toBeEmptyDOMElement();

    api.me = down();
    await refresh(client);
    await waitFor(() => {
      expect(line).toHaveTextContent("Can't reach the machine. Retrying…");
    });
    expect(screen.getByText("The page")).toBeVisible();
    // Not yet a reason to ask for a click: the shell is trying again by itself.
    expect(screen.queryByRole("button", { name: "Try again" })).toBeNull();

    api.me = good;
    await refresh(client);
    await waitFor(() => {
      expect(line).toBeEmptyDOMElement();
    });
  });

  it("offers Try again after three failures in a row, and it clears when the machine answers", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    const good = api.me;
    const client = renderShell(api);
    expect(await screen.findByText("The page")).toBeVisible();

    api.me = down();
    for (let failure = 1; failure <= 2; failure++) {
      await refresh(client);
      await screen.findByText("Can't reach the machine. Retrying…");
      expect(screen.queryByRole("button", { name: "Try again" })).toBeNull();
    }
    await refresh(client);
    expect(await screen.findByRole("button", { name: "Try again" })).toBeVisible();

    api.me = good;
    await user.click(screen.getByRole("button", { name: "Try again" }));
    await waitFor(() => {
      expect(screen.getByRole("status")).toBeEmptyDOMElement();
    });
    expect(screen.queryByRole("button", { name: "Try again" })).toBeNull();
  });

  it("marks the page while stale, so what is live can be dimmed", async () => {
    const api = new FakeApi();
    const client = renderShell(api);
    const page = (await screen.findByText("The page")).closest("main");
    expect(page).not.toHaveAttribute("data-stale");

    api.me = down();
    await refresh(client);
    await waitFor(() => {
      expect(page).toHaveAttribute("data-stale", "true");
    });
  });

  it("shows the error when the first load fails", async () => {
    const api = new FakeApi();
    api.me = down();
    renderShell(api);

    expect(await screen.findByRole("alert")).toBeVisible();
    expect(screen.queryByText("The page")).toBeNull();
  });
});
