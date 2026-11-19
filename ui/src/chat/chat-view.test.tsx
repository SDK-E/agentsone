// SPDX-License-Identifier: MIT
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoAxeViolations } from "../test/axe";
import { FakeApi, fixtures, problem, renderApp } from "../test/fake-api";
import { mergeMessage } from "./use-chat";

const messages = () => screen.findByRole("region", { name: "Messages" });

describe("ChatView", () => {
  it("shows the agent and the chat in time order, with you, the agent and the system", async () => {
    renderApp("/agents/builder/chat");

    expect(await screen.findByRole("heading", { level: 1, name: "Builder" })).toBeVisible();
    const region = await messages();
    const items = await within(region).findAllByRole("listitem");
    const [human, agent, system] = items;
    if (!human || !agent || !system) throw new Error("expected three messages");
    expect(items).toHaveLength(3);
    expect(within(human).getByText(/^You/)).toBeVisible();
    expect(within(agent).getByText(/^Builder/)).toBeVisible();
    // The locale's own hour format: "6:20 PM", never "06:20 PM".
    expect(within(agent).getByText(/^[1-9]\d?:\d\d\sPM$/)).toBeVisible();
    expect(system).toHaveTextContent('builder created task "Budget walls page"');
  });

  it("sends a message and clears the box", async () => {
    const user = userEvent.setup();
    const { api } = renderApp("/agents/builder/chat");
    const field = await screen.findByRole("textbox", { name: "Message Builder" });

    await user.type(field, "Thanks{Enter}");

    await waitFor(() => {
      expect(field).toHaveValue("");
    });
    expect(api.calls).toContain("sendChatMessage builder Thanks");
    expect(await screen.findByText("Thanks")).toBeVisible();
  });

  it("labels the budget wall as hard and held by the control page", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.sendError = problem(402, "Budget used up");
    renderApp("/agents/builder/chat", api);

    await user.type(await screen.findByRole("textbox", { name: "Message Builder" }), "Hi{Enter}");

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Budget used up");
    expect(alert).toHaveTextContent("This is a hard wall, held by the control page.");
  });

  it("says a frozen agent is released from the control page", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.sendError = problem(423, "Frozen");
    renderApp("/agents/builder/chat", api);

    await user.type(await screen.findByRole("textbox", { name: "Message Builder" }), "Hi{Enter}");

    expect(await screen.findByRole("alert")).toHaveTextContent("Release it from the control page");
  });

  it("shows a reply as the agent writes it, then the finished message", async () => {
    const api = new FakeApi();
    renderApp("/agents/builder/chat", api);
    await within(await messages()).findAllByRole("listitem");

    act(() => {
      api.chatStream.emit({
        kind: "delta",
        message_id: "cht_01JD7ZK3M0000000000000000A",
        text: "Draft ",
      });
      api.chatStream.emit({
        kind: "delta",
        message_id: "cht_01JD7ZK3M0000000000000000A",
        text: "is up.",
      });
    });
    expect(screen.getByText("Builder is writing…")).toBeVisible();
    expect(screen.getByText("Draft is up.")).toBeVisible();

    act(() => {
      api.chatStream.emit({
        kind: "message",
        message: {
          id: "cht_01JD7ZK3M0000000000000000A",
          agent: "builder",
          role: "agent",
          author: "agent:builder",
          text: "Draft is up.",
          assignment_id: null,
          created_at: "2026-11-19T18:30:00Z",
        },
      });
    });
    expect(screen.queryByText("Builder is writing…")).not.toBeInTheDocument();
    expect(screen.getByText("Draft is up.")).toBeVisible();
  });

  it("shows what the agent is doing from its output stream", async () => {
    const api = new FakeApi();
    renderApp("/agents/builder/chat", api);
    await messages();

    act(() => {
      api.outputStream.emit({
        kind: "state",
        agent: "builder",
        state: "sleeping",
        time: "2026-11-19T18:41:00Z",
      });
      api.outputStream.emit({
        kind: "output",
        agent: "builder",
        stream: "tool",
        text: "write docs/budget.md",
        time: "2026-11-19T18:41:01Z",
      });
    });

    const activity = screen.getByRole("region", { name: "What Builder is doing" });
    expect(within(activity).getByText("write docs/budget.md")).toBeVisible();
    expect(screen.getByText("Asleep")).toBeVisible();
  });

  it("falls back to polling when live replies are not available", async () => {
    const api = new FakeApi();
    renderApp("/agents/builder/chat", api);
    await messages();

    act(() => {
      api.chatStream.close();
    });

    expect(screen.getByText(/Live replies are not available/)).toBeVisible();
  });

  it("shows an empty chat", async () => {
    const api = new FakeApi();
    api.chat = [];
    renderApp("/agents/builder/chat", api);

    expect(await screen.findByRole("heading", { name: "No messages yet" })).toBeVisible();
  });

  it("shows a load error with a retry", async () => {
    const api = new FakeApi();
    api.chat = problem(404, "No such agent");
    renderApp("/agents/nobody/chat", api);

    expect(await screen.findByRole("alert")).toHaveTextContent("No such agent");
  });

  it("has no accessibility violations", async () => {
    const { container } = renderApp("/agents/builder/chat");
    await within(await messages()).findAllByRole("listitem");

    await expectNoAxeViolations(container);
  });
});

describe("mergeMessage", () => {
  it("replaces a message with the same id and keeps time order", () => {
    const [a, b] = fixtures.chat;
    if (!a || !b) throw new Error("fixture too short");

    const merged = mergeMessage([b], { ...a, text: "edited" });

    expect(merged.map((m) => m.text)).toEqual(["edited", b.text]);
    expect(mergeMessage(merged, { ...a, text: "again" })).toHaveLength(2);
  });
});
