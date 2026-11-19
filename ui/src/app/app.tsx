// SPDX-License-Identifier: MIT
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import { I18nextProvider } from "react-i18next";
import { Route, Routes } from "react-router";

import type { Api } from "../api/api";
import { ChatView } from "../chat/chat-view";
import { i18n } from "../i18n/i18n";
import { SignInScreen } from "../session/sign-in-screen";
import { SetupScreen } from "../setup/setup-screen";
import { AppShell } from "../shell/app-shell";
import { Home } from "../shell/home";
import { NotFound } from "../shell/not-found";
import { ThemeProvider } from "../theme/theme-provider";
import { ApiProvider } from "./api-context";

export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: { queries: { staleTime: 5_000, refetchOnWindowFocus: false } },
  });
}

/** Everything the screens need, without the browser router (tests bring a memory router). */
export function AppProviders({ api, children }: { api: Api; children: ReactNode }) {
  const [client] = useState(createQueryClient);
  return (
    <I18nextProvider i18n={i18n}>
      <ApiProvider api={api}>
        <QueryClientProvider client={client}>
          <ThemeProvider>{children}</ThemeProvider>
        </QueryClientProvider>
      </ApiProvider>
    </I18nextProvider>
  );
}

export function AppRoutes() {
  return (
    <Routes>
      <Route path="/setup" element={<SetupScreen />} />
      <Route path="/sign-in" element={<SignInScreen />} />
      <Route
        path="/"
        element={
          <AppShell>
            <Home />
          </AppShell>
        }
      />
      <Route
        path="/agents/:name/chat"
        element={
          <AppShell>
            <ChatView />
          </AppShell>
        }
      />
      <Route path="*" element={<NotFound />} />
    </Routes>
  );
}
