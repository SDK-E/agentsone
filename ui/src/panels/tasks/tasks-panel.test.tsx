// SPDX-License-Identifier: MIT
import { act, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { expectNoAxeViolations } from "../../test/axe";
import { FakeApi, fixtures, problem, renderWith } from "../../test/fake-api";
import TasksPanel from "./index";

const taskList = () => screen.findByRole("list", { name: "Tasks, newest first" });
const rowFor = async (title: string) => {
  const items = within(await taskList()).getAllByRole("listitem");
  const row = items.find((li) => within(li).queryByText(title));
  if (!row) throw new Error(`no row for ${title}`);
  return row;
};

describe("TasksPanel list", () => {
  it("shows each task with its status, priority and who holds it", async () => {
    renderWith(<TasksPanel />);

    const row = await rowFor("Budget walls page");
    expect(within(row).getByText("In progress")).toBeVisible();
    expect(within(row).getByText("Normal priority")).toBeVisible();
    expect(within(row).getByText("With Scribe")).toBeVisible();
    const unheld = await rowFor("Morning health report");
    expect(within(unheld).getByText("Blocked")).toBeVisible();
    expect(within(unheld).getByText("Nobody has it yet")).toBeVisible();
  });

  it("filters by status through the daemon", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    renderWith(<TasksPanel />, api);
    await taskList();

    await user.selectOptions(screen.getByRole("combobox", { name: "Status" }), "done");

    expect(await screen.findByText("Disk / above 80 percent")).toBeVisible();
    expect(screen.queryByText("Budget walls page")).not.toBeInTheDocument();
    expect(api.calls).toContain("listTasks done anyone");
  });

  it("filters by who holds the task", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    renderWith(<TasksPanel />, api);
    await taskList();

    await user.selectOptions(screen.getByRole("combobox", { name: "Held by" }), "builder");

    expect(await screen.findByText("Add a page about the budget walls")).toBeVisible();
    expect(screen.queryByText("Budget walls page")).not.toBeInTheDocument();
  });

  it("says so, and offers to clear the filters, when nothing matches", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    renderWith(<TasksPanel />, api);
    await taskList();

    await user.selectOptions(screen.getByRole("combobox", { name: "Status" }), "failed");

    expect(await screen.findByRole("heading", { name: "No tasks match" })).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Show all tasks" }));
    expect(await screen.findByText("Budget walls page")).toBeVisible();
  });

  it("invites the first task when there are none", async () => {
    const api = new FakeApi();
    api.tasks = [];
    renderWith(<TasksPanel />, api);

    expect(await screen.findByRole("heading", { name: "No tasks yet" })).toBeVisible();
    expect(screen.getByRole("button", { name: "New task" })).toBeEnabled();
  });

  it("says what went wrong and offers a retry", async () => {
    const api = new FakeApi();
    api.tasks = problem(500, "Broken", "The task list could not be read.");
    renderWith(<TasksPanel />, api);

    expect(await screen.findByRole("alert")).toHaveTextContent("The task list could not be read.");
    expect(screen.getByRole("button", { name: "Try again" })).toBeEnabled();
  });

  it("loads the next page on request", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    const [first, second] = fixtures.tasks;
    if (!first || !second) throw new Error("fixture needs two tasks");
    api.listTasks = (_filter, cursor) =>
      Promise.resolve(
        cursor === undefined
          ? { items: [first], next_cursor: "page-2" }
          : { items: [second], next_cursor: null },
      );
    renderWith(<TasksPanel />, api);
    await taskList();
    expect(screen.queryByText(second.title)).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Show more tasks" }));

    expect(await screen.findByText(second.title)).toBeVisible();
    expect(screen.queryByRole("button", { name: "Show more tasks" })).not.toBeInTheDocument();
  });

  it("updates a task in place when the stream reports a change", async () => {
    const api = new FakeApi();
    renderWith(<TasksPanel />, api);
    const row = await rowFor("Add a page about the budget walls");
    expect(within(row).getByText("Open")).toBeVisible();
    const task = fixtures.tasks[2];
    if (!task) throw new Error("fixture needs three tasks");

    act(() => {
      api.taskStream.emit({
        kind: "task",
        task: { ...task, status: "done", result: "Page shipped." },
      });
    });

    expect(
      within(await rowFor("Add a page about the budget walls")).getByText("Done"),
    ).toBeVisible();
  });

  it("keeps very long titles and details inside the row", async () => {
    const api = new FakeApi();
    const first = fixtures.tasks[0];
    if (!first) throw new Error("no fixture");
    api.tasks = [
      {
        ...first,
        title: "An extremely long task title ".repeat(7).trim(),
        detail: "Many words ".repeat(300),
      },
    ];
    renderWith(<TasksPanel />, api);

    expect(await screen.findByText(/An extremely long task title/)).toBeVisible();
  });
});

describe("TasksPanel detail", () => {
  it("opens a task with its wording, outcome and history", async () => {
    const user = userEvent.setup();
    renderWith(<TasksPanel />);
    const row = await rowFor("Disk / above 80 percent");

    await user.click(within(row).getByRole("button", { name: /Disk \/ above 80 percent/ }));

    const detail = await screen.findByRole("region", { name: "Disk / above 80 percent" });
    expect(within(detail).getByText(/Created by trigger "disk pressure"/)).toBeVisible();
    expect(within(detail).getByText(/Removed 1.4 GiB of old journal files/)).toBeVisible();
    expect(within(detail).getByText("Outcome")).toBeVisible();
    expect(within(detail).getByText("Held by")).toBeVisible();
    expect(within(detail).getByText("Keeper")).toBeVisible();
    expect(within(detail).getByText("Started by an event")).toBeVisible();

    await user.click(screen.getByRole("button", { name: "Back to tasks" }));
    expect(await taskList()).toBeVisible();
  });
});

describe("TasksPanel new task", () => {
  const open = async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    renderWith(<TasksPanel />, api);
    await taskList();
    await user.click(screen.getByRole("button", { name: "New task" }));
    return { user, api, form: await screen.findByRole("form", { name: "New task" }) };
  };

  it("creates a task for an agent and shows it in the list", async () => {
    const { user, api, form } = await open();

    await user.type(
      within(form).getByRole("textbox", { name: "What needs doing" }),
      "Link the budget page",
    );
    await user.selectOptions(within(form).getByRole("combobox", { name: "Give it to" }), "builder");
    await user.click(within(form).getByRole("button", { name: "Create task" }));

    expect(await screen.findByText("Link the budget page")).toBeVisible();
    expect(api.calls).toContain("createTask Link the budget page builder");
    expect(screen.getByRole("status")).toHaveTextContent("Task created.");
    expect(screen.queryByRole("form", { name: "New task" })).not.toBeInTheDocument();
  });

  it("asks for a title before sending anything", async () => {
    const { user, api, form } = await open();

    await user.click(within(form).getByRole("button", { name: "Create task" }));

    expect(within(form).getByText("Say what needs doing.")).toBeVisible();
    expect(api.calls.some((c) => c.startsWith("createTask"))).toBe(false);
  });

  it("shows the daemon's own words plainly when the person may not assign it", async () => {
    const { user, api, form } = await open();
    api.createError = problem(
      403,
      "Agent is not below you",
      "You may only give tasks to agents below you; keeper is in another team.",
    );

    await user.type(within(form).getByRole("textbox", { name: "What needs doing" }), "Trim logs");
    await user.selectOptions(within(form).getByRole("combobox", { name: "Give it to" }), "keeper");
    await user.click(within(form).getByRole("button", { name: "Create task" }));

    const alert = await within(form).findByRole("alert");
    expect(alert).toHaveTextContent("You are not allowed to give this task to that agent.");
    expect(alert).toHaveTextContent("keeper is in another team");
    expect(within(form).getByRole("textbox", { name: "What needs doing" })).toHaveValue(
      "Trim logs",
    );
  });

  it("explains a frozen agent", async () => {
    const { user, api, form } = await open();
    api.createError = problem(
      423,
      "Frozen",
      "keeper is frozen and cannot take tasks until it is released.",
    );

    await user.type(within(form).getByRole("textbox", { name: "What needs doing" }), "Trim logs");
    await user.click(within(form).getByRole("button", { name: "Create task" }));

    expect(await within(form).findByRole("alert")).toHaveTextContent("That agent is frozen");
  });

  it("falls back to a general message for other failures", async () => {
    const { user, api, form } = await open();
    api.createError = problem(500, "Broken");

    await user.type(within(form).getByRole("textbox", { name: "What needs doing" }), "Trim logs");
    await user.click(within(form).getByRole("button", { name: "Create task" }));

    expect(await within(form).findByRole("alert")).toHaveTextContent("The task was not created.");
  });

  it("closes without creating when cancelled", async () => {
    const { user, form } = await open();

    await user.click(within(form).getByRole("button", { name: "Cancel" }));

    expect(screen.queryByRole("form", { name: "New task" })).not.toBeInTheDocument();
  });
});

describe("TasksPanel schedules", () => {
  const openSchedules = async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    renderWith(<TasksPanel />, api);
    await taskList();
    await user.click(screen.getByRole("button", { name: "Schedules" }));
    return { user, api };
  };

  it("shows each schedule in plain words with its next and last run", async () => {
    await openSchedules();

    const list = await screen.findByRole("list", { name: "Schedules" });
    const items = within(list).getAllByRole("listitem");
    const morning = items[0];
    const weekly = items[1];
    if (!morning || !weekly) throw new Error("fixture needs two schedules");
    expect(within(morning).getByText("Morning health report")).toBeVisible();
    expect(within(morning).getByText("Every day at 09:00 (UTC)")).toBeVisible();
    expect(within(morning).getByText("On")).toBeVisible();
    expect(within(morning).getByText(/Next run/)).toBeVisible();
    expect(within(weekly).getByText("Every Monday at 03:00 (UTC)")).toBeVisible();
    expect(within(weekly).getByText("Paused")).toBeVisible();
    expect(within(weekly).getByText("Has not run yet")).toBeVisible();
  });

  it("shows a calendar it cannot put in words exactly as written", async () => {
    const api = new FakeApi();
    const first = fixtures.schedules[0];
    if (!first) throw new Error("no fixture");
    api.schedules = [{ ...first, calendar: "Mon..Fri *-*-* 08:30:00" }];
    const user = userEvent.setup();
    renderWith(<TasksPanel />, api);
    await taskList();
    await user.click(screen.getByRole("button", { name: "Schedules" }));

    expect(await screen.findByText("Mon..Fri *-*-* 08:30:00")).toBeVisible();
  });

  it("is honest when there are no schedules", async () => {
    const api = new FakeApi();
    api.schedules = [];
    const user = userEvent.setup();
    renderWith(<TasksPanel />, api);
    await taskList();
    await user.click(screen.getByRole("button", { name: "Schedules" }));

    expect(await screen.findByRole("heading", { name: "No schedules yet" })).toBeVisible();
  });

  it("says what went wrong", async () => {
    const api = new FakeApi();
    api.schedules = problem(500, "Broken", "The timers could not be read.");
    const user = userEvent.setup();
    renderWith(<TasksPanel />, api);
    await taskList();
    await user.click(screen.getByRole("button", { name: "Schedules" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("The timers could not be read.");
  });
});

describe("TasksPanel accessibility", () => {
  it("has no violations in the list, the form and the schedules", async () => {
    const user = userEvent.setup();
    const { container } = renderWith(<TasksPanel />);
    await taskList();
    await expectNoAxeViolations(container);

    await user.click(screen.getByRole("button", { name: "New task" }));
    await screen.findByRole("form", { name: "New task" });
    await expectNoAxeViolations(container);

    await user.click(screen.getByRole("button", { name: "Cancel" }));
    await user.click(screen.getByRole("button", { name: "Schedules" }));
    await screen.findByRole("list", { name: "Schedules" });
    await expectNoAxeViolations(container);
  });
});
