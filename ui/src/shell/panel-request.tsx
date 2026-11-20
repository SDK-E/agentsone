// SPDX-License-Identifier: MIT
//
// A way for something on the home screen to open a panel at one thing in it: the society's
// safety chip opens the Machine panel at the safety notice. The panels area opens (as a sheet on
// a phone), the tab turns, and the thing named by `target` brings itself into view once.
import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";

import type { PanelId } from "../panels/registry";

export interface PanelRequest {
  panel: PanelId;
  /** What to bring into view inside the panel, such as a safety level. */
  target: string | undefined;
  /** Tells two requests for the same place apart. */
  seq: number;
}

interface PanelRequests {
  request: PanelRequest | undefined;
  openPanel: (panel: PanelId, target?: string) => void;
  /** True once per request, for the first caller that names its target. */
  take: (target: string) => boolean;
}

const Context = createContext<PanelRequests>({
  request: undefined,
  openPanel: () => undefined,
  take: () => false,
});

export function PanelRequestProvider({ children }: { children: ReactNode }) {
  const [request, setRequest] = useState<PanelRequest>();
  const taken = useRef(0);
  const openPanel = useCallback((panel: PanelId, target?: string) => {
    setRequest((old) => ({ panel, target, seq: (old?.seq ?? 0) + 1 }));
  }, []);
  const take = useCallback(
    (target: string) => {
      if (request?.target !== target || taken.current === request.seq) return false;
      taken.current = request.seq;
      return true;
    },
    [request],
  );
  const value = useMemo(() => ({ request, openPanel, take }), [request, openPanel, take]);
  return <Context.Provider value={value}>{children}</Context.Provider>;
}

export function usePanelRequest(): PanelRequests {
  return useContext(Context);
}
