// SPDX-License-Identifier: MIT
import { useQuery, useMutation } from "@tanstack/react-query";
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";

import { useApi } from "../app/api-context";
import { useMediaQuery } from "../shell/use-media-query";
import {
  applyTheme,
  defaultTheme,
  resolveMode,
  type ResolvedMode,
  type Theme,
  type ThemeMode,
} from "./theme";

const STORAGE_KEY = "agentsone.mode";
const MODES: readonly ThemeMode[] = ["system", "light", "dark"];

function storedMode(): ThemeMode {
  try {
    const value = window.localStorage.getItem(STORAGE_KEY);
    return MODES.find((m) => m === value) ?? "system";
  } catch {
    return "system";
  }
}

function storeMode(mode: ThemeMode): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, mode);
  } catch {
    // Storage can be blocked; the daemon keeps the choice for signed-in humans anyway.
  }
}

interface ThemeState {
  mode: ThemeMode;
  resolved: ResolvedMode;
  theme: Theme;
  setMode: (mode: ThemeMode) => void;
  setTheme: (theme: Theme) => void;
}

const ThemeContext = createContext<ThemeState | null>(null);

/** Applies the theme to the document root and follows the system light or dark setting. */
export function ThemeProvider({ children }: { children: ReactNode }) {
  const prefersDark = useMediaQuery("(prefers-color-scheme: dark)");
  const [mode, setModeState] = useState<ThemeMode>(storedMode);
  const [theme, setTheme] = useState<Theme>(defaultTheme);
  const resolved = resolveMode(mode, prefersDark);

  useLayoutEffect(() => {
    applyTheme(document.documentElement, theme, resolved);
  }, [theme, resolved]);

  const setMode = useCallback((next: ThemeMode) => {
    storeMode(next);
    setModeState(next);
  }, []);
  const value = useMemo<ThemeState>(
    () => ({ mode, resolved, theme, setTheme, setMode }),
    [mode, resolved, theme, setMode],
  );
  return <ThemeContext value={value}>{children}</ThemeContext>;
}

export function useTheme(): ThemeState {
  const state = useContext(ThemeContext);
  if (!state) throw new Error("useTheme needs a ThemeProvider above it");
  return state;
}

/**
 * For a signed-in human: load their saved theme and mode from the daemon, and save changes.
 * The bundled Agents One Theme stays the source for the built-in theme.
 */
export function useThemeSelection(): { mode: ThemeMode; choose: (mode: ThemeMode) => void } {
  const api = useApi();
  const { mode, setMode, setTheme } = useTheme();
  const selection = useQuery({
    queryKey: ["theme-selection"],
    queryFn: () => api.getThemeSelection(),
    retry: false,
  });
  const themeId = selection.data?.theme_id;
  const installed = useQuery({
    queryKey: ["theme", themeId],
    queryFn: () => api.getTheme(themeId ?? ""),
    enabled: themeId !== undefined,
    retry: false,
  });
  const save = useMutation({ mutationFn: (m: ThemeMode) => api.setThemeSelection({ mode: m }) });

  // The saved mode wins once, on load, unless the person already picked one on this screen.
  const chosen = useRef(false);
  const savedMode = selection.data?.mode;
  useEffect(() => {
    if (savedMode && !chosen.current) setMode(savedMode);
  }, [savedMode, setMode]);

  const installedTheme = installed.data;
  useEffect(() => {
    // The API and the theme schema describe the same document; the schema's tuple type for
    // contrastPairs is stricter than the API's array type.
    if (installedTheme && !installedTheme.builtin) setTheme(installedTheme.theme as Theme);
  }, [installedTheme, setTheme]);

  return {
    mode,
    choose: (next) => {
      chosen.current = true;
      setMode(next);
      save.mutate(next);
    },
  };
}
