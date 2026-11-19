// SPDX-License-Identifier: MIT
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";

import type { AgentOutputFrame, ChatMessage, LiveEntry } from "../api/api";
import { useApi } from "../app/api-context";

const POLL_MS = 5_000;
const ACTIVITY_LINES = 20;

/** Add or replace a message and keep the chat in time order, oldest first. */
export function mergeMessage(list: ChatMessage[], message: ChatMessage): ChatMessage[] {
  return [...list.filter((m) => m.id !== message.id), message].sort((a, b) =>
    a.created_at.localeCompare(b.created_at),
  );
}

/** One agent's chat: history, live replies (with partial text while it writes) and sending. */
export function useChat(agent: string) {
  const api = useApi();
  const client = useQueryClient();
  const key = ["chat", agent];
  const [polling, setPolling] = useState(false);
  const [drafts, setDrafts] = useState<Record<string, string>>({});

  const messages = useQuery({
    queryKey: key,
    queryFn: async () =>
      (await api.listChatMessages(agent)).toSorted((a, b) =>
        a.created_at.localeCompare(b.created_at),
      ),
    retry: false,
    refetchInterval: polling ? POLL_MS : false,
  });

  useEffect(() => {
    setPolling(false);
    setDrafts({});
    return api.streamChat(agent, {
      onFrame: (frame) => {
        if (frame.kind === "message") {
          const done = frame.message;
          client.setQueryData<ChatMessage[]>(["chat", agent], (old) =>
            mergeMessage(old ?? [], done),
          );
          setDrafts((d) => Object.fromEntries(Object.entries(d).filter(([id]) => id !== done.id)));
        } else if (frame.kind === "delta") {
          setDrafts((d) => ({ ...d, [frame.message_id]: (d[frame.message_id] ?? "") + frame.text }));
        }
      },
      onClose: () => {
        setPolling(true);
      },
    });
  }, [api, client, agent]);

  const send = useMutation({
    mutationFn: (text: string) => api.sendChatMessage(agent, text),
    onSuccess: (message) => {
      client.setQueryData<ChatMessage[]>(["chat", agent], (old) =>
        mergeMessage(old ?? [], message),
      );
    },
  });

  return { messages, send, drafts: Object.values(drafts), polling };
}

/** What the agent is doing right now: its state and its latest runner output. */
export function useAgentActivity(agent: string, entry: LiveEntry | undefined) {
  const api = useApi();
  const [state, setState] = useState<LiveEntry["state"] | undefined>(undefined);
  const [lines, setLines] = useState<Extract<AgentOutputFrame, { kind: "output" }>[]>([]);

  useEffect(() => {
    setState(undefined);
    setLines([]);
    return api.streamAgentOutput(agent, {
      onFrame: (frame) => {
        if (frame.kind === "state") setState(frame.state);
        else if (frame.kind === "output") {
          setLines((old) => [...old, frame].slice(-ACTIVITY_LINES));
        }
      },
    });
  }, [api, agent]);

  return { state: state ?? entry?.state, lines };
}
