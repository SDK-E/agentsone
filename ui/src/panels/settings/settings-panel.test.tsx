// SPDX-License-Identifier: MIT
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { ApiError, type Human, type InstalledTheme } from "../../api/api";
import { at } from "../../test/at";
import { expectNoAxeViolations } from "../../test/axe";
import { FakeApi, fixtures, problem, renderWith } from "../../test/fake-api";
import SettingsPanel from ".";

// Test values only. These never leave the test run.
const GOOD_PASSWORD = "test-password-12345";
const CURRENT_PASSWORD = "old-password-12345";

const region = (name: string) => screen.findByRole("region", { name });

function acceptedApi() {
  const api = new FakeApi();
  api.licence = { ...fixtures.licence };
  return api;
}

function otherTheme(): InstalledTheme {
  const base = fixtures.theme;
  return {
    ...base,
    id: "thm_01JD7ZK3M00000000000000002",
    name: "Night Owl",
    builtin: false,
    theme: {
      ...base.theme,
      name: "Night Owl",
      modes: {
        ...base.theme.modes,
        light: {
          ...base.theme.modes.light,
          color: { ...base.theme.modes.light.color, background: "#FAFAFA" },
        },
      },
    },
  };
}

describe("Settings, look and feel", () => {
  it("lists installed themes and marks the one in use", async () => {
    const api = new FakeApi();
    api.themes = [fixtures.theme, otherTheme()];
    renderWith(<SettingsPanel />, api);

    const look = within(await region("Look and feel"));
    expect(await look.findByRole("radio", { name: "Agents One Theme" })).toBeChecked();
    expect(look.getByRole("radio", { name: "Night Owl" })).not.toBeChecked();
  });

  it("saves a new theme and applies it right away", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    const owl = otherTheme();
    api.themes = [fixtures.theme, owl];
    renderWith(<SettingsPanel />, api);

    await user.click(await screen.findByRole("radio", { name: "Night Owl" }));

    expect(screen.getByRole("radio", { name: "Night Owl" })).toBeChecked();
    await waitFor(() => {
      expect(api.calls).toContain(`setThemeSelection ${owl.id}`);
    });
    expect(document.documentElement.style.getPropertyValue("--color-background")).toBe("#FAFAFA");
  });

  it("says where more themes come from when only the built-in one exists", async () => {
    renderWith(<SettingsPanel />);

    expect(await screen.findByText(/Install a theme from the Plugins panel/)).toBeVisible();
  });

  it("explains a failed theme save in the daemon's words", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.themes = [fixtures.theme, otherTheme()];
    api.mutationError = problem(503, "Unavailable", "The theme store is busy.");
    renderWith(<SettingsPanel />, api);

    await user.click(await screen.findByRole("radio", { name: "Night Owl" }));

    expect(await screen.findByText("The theme store is busy.")).toBeVisible();
  });
});

describe("Settings, your account", () => {
  it("shows who you are", async () => {
    renderWith(<SettingsPanel />);

    const account = within(await region("Your account"));
    expect(await account.findByText("ada")).toBeVisible();
    expect(account.getByRole("textbox", { name: "Display name" })).toHaveValue("Ada Lovelace");
  });

  it("saves a new display name", async () => {
    const user = userEvent.setup();
    const { api } = renderWith(<SettingsPanel />);
    const account = within(await region("Your account"));
    const field = await account.findByRole("textbox", { name: "Display name" });

    await user.clear(field);
    await user.type(field, "Ada King");
    await user.click(account.getByRole("button", { name: "Save name" }));

    expect(api.calls).toContain("updateHuman ada display_name");
    expect(await account.findByText("Name saved")).toBeVisible();
  });

  it("does not save an empty display name", async () => {
    const user = userEvent.setup();
    const { api } = renderWith(<SettingsPanel />);
    const account = within(await region("Your account"));

    await user.clear(await account.findByRole("textbox", { name: "Display name" }));

    expect(account.getByRole("button", { name: "Save name" })).toBeDisabled();
    expect(api.calls.filter((c) => c.startsWith("updateHuman"))).toEqual([]);
  });

  it("tells you that a new password signs you out everywhere else", async () => {
    renderWith(<SettingsPanel />);

    expect(await screen.findByText(/signs you out everywhere else/)).toBeVisible();
  });

  it("changes the password", async () => {
    const user = userEvent.setup();
    const { api } = renderWith(<SettingsPanel />);
    const account = within(await region("Your account"));

    await user.type(await account.findByLabelText("Current password"), CURRENT_PASSWORD);
    await user.type(account.getByLabelText("New password"), GOOD_PASSWORD);
    await user.type(account.getByLabelText("New password again"), GOOD_PASSWORD);
    await user.click(account.getByRole("button", { name: "Change password" }));

    expect(api.calls).toContain("updateHuman ada current_password,password");
    expect(await account.findByText(/Password changed/)).toBeVisible();
    expect(account.getByLabelText("Current password")).toHaveValue("");
    expect(account.getByLabelText("New password")).toHaveValue("");
    expect(account.getByLabelText("New password again")).toHaveValue("");
  });

  it("asks for the current password before sending anything", async () => {
    const user = userEvent.setup();
    const { api } = renderWith(<SettingsPanel />);
    const account = within(await region("Your account"));

    await user.type(await account.findByLabelText("New password"), GOOD_PASSWORD);
    await user.type(account.getByLabelText("New password again"), GOOD_PASSWORD);
    await user.click(account.getByRole("button", { name: "Change password" }));

    expect(await account.findByRole("alert")).toHaveTextContent("Type your current password.");
    expect(api.calls.filter((c) => c.startsWith("updateHuman"))).toEqual([]);
  });

  it("says when the current password is wrong and keeps what was typed", async () => {
    const user = userEvent.setup();
    renderWith(<SettingsPanel />);
    const account = within(await region("Your account"));

    await user.type(await account.findByLabelText("Current password"), "not-the-password-1");
    await user.type(account.getByLabelText("New password"), GOOD_PASSWORD);
    await user.type(account.getByLabelText("New password again"), GOOD_PASSWORD);
    await user.click(account.getByRole("button", { name: "Change password" }));

    expect(await account.findByRole("alert")).toHaveTextContent(
      "That is not your current password. Check it and try again.",
    );
    expect(account.getByLabelText("New password")).toHaveValue(GOOD_PASSWORD);
  });

  it("says how long to wait after too many wrong passwords", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.mutationError = new ApiError(
      429,
      { type: "urn:agentsone:problem:rate-limited", title: "Too many requests", status: 429 },
      30,
    );
    renderWith(<SettingsPanel />, api);
    const account = within(await region("Your account"));

    await user.type(await account.findByLabelText("Current password"), CURRENT_PASSWORD);
    await user.type(account.getByLabelText("New password"), GOOD_PASSWORD);
    await user.type(account.getByLabelText("New password again"), GOOD_PASSWORD);
    await user.click(account.getByRole("button", { name: "Change password" }));

    expect(await account.findByRole("alert")).toHaveTextContent(
      "Too many wrong passwords. Wait 30 seconds.",
    );
  });

  it("says to wait one second in the singular", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.mutationError = new ApiError(
      429,
      { type: "urn:agentsone:problem:rate-limited", title: "Too many requests", status: 429 },
      1,
    );
    renderWith(<SettingsPanel />, api);
    const account = within(await region("Your account"));

    await user.type(await account.findByLabelText("Current password"), CURRENT_PASSWORD);
    await user.type(account.getByLabelText("New password"), GOOD_PASSWORD);
    await user.type(account.getByLabelText("New password again"), GOOD_PASSWORD);
    await user.click(account.getByRole("button", { name: "Change password" }));

    expect(await account.findByRole("alert")).toHaveTextContent(
      "Too many wrong passwords. Wait 1 second.",
    );
  });

  it("asks to wait a little when the throttle gives no time", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.mutationError = problem(429, "Too many requests");
    renderWith(<SettingsPanel />, api);
    const account = within(await region("Your account"));

    await user.type(await account.findByLabelText("Current password"), CURRENT_PASSWORD);
    await user.type(account.getByLabelText("New password"), GOOD_PASSWORD);
    await user.type(account.getByLabelText("New password again"), GOOD_PASSWORD);
    await user.click(account.getByRole("button", { name: "Change password" }));

    expect(await account.findByRole("alert")).toHaveTextContent(
      "Too many wrong passwords. Wait a little, then try again.",
    );
  });

  it("asks for a longer password before sending anything", async () => {
    const user = userEvent.setup();
    const { api } = renderWith(<SettingsPanel />);
    const account = within(await region("Your account"));

    await user.type(await account.findByLabelText("New password"), "short");
    await user.type(account.getByLabelText("New password again"), "short");
    await user.click(account.getByRole("button", { name: "Change password" }));

    expect(await account.findByRole("alert")).toHaveTextContent(
      "The password needs at least 12 characters.",
    );
    expect(api.calls.filter((c) => c.startsWith("updateHuman"))).toEqual([]);
  });

  it("asks for the same password twice", async () => {
    const user = userEvent.setup();
    const { api } = renderWith(<SettingsPanel />);
    const account = within(await region("Your account"));

    await user.type(await account.findByLabelText("New password"), GOOD_PASSWORD);
    await user.type(account.getByLabelText("New password again"), `${GOOD_PASSWORD}x`);
    await user.click(account.getByRole("button", { name: "Change password" }));

    expect(await account.findByRole("alert")).toHaveTextContent(
      "The two passwords are not the same.",
    );
    expect(api.calls.filter((c) => c.startsWith("updateHuman"))).toEqual([]);
  });

  it("shows why the password was not changed", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.mutationError = problem(422, "Weak", "That password is too easy to guess.");
    renderWith(<SettingsPanel />, api);
    const account = within(await region("Your account"));

    await user.type(await account.findByLabelText("Current password"), CURRENT_PASSWORD);
    await user.type(account.getByLabelText("New password"), GOOD_PASSWORD);
    await user.type(account.getByLabelText("New password again"), GOOD_PASSWORD);
    await user.click(account.getByRole("button", { name: "Change password" }));

    expect(await account.findByText("That password is too easy to guess.")).toBeVisible();
    expect(account.getByLabelText("New password")).toHaveValue(GOOD_PASSWORD);
  });

  it("explains a failure to load the account", async () => {
    const api = new FakeApi();
    api.me = problem(503, "Unavailable", "The account store is starting.");
    renderWith(<SettingsPanel />, api);

    const account = within(await region("Your account"));
    expect(await account.findByRole("alert")).toHaveTextContent("The account store is starting.");
  });
});

describe("Settings, people", () => {
  it("lists the people, marks you and shows who never signed in", async () => {
    renderWith(<SettingsPanel />);

    const people = within(await region("People"));
    const list = await people.findByRole("list", { name: "People" });
    const rows = Array.from(list.children) as HTMLElement[];
    const ada = within(at(rows, 0));
    const grace = within(at(rows, 1));
    expect(ada.getByText("Ada Lovelace")).toBeVisible();
    expect(ada.getByText("You")).toBeVisible();
    expect(ada.queryByRole("button", { name: /^Remove/ })).toBeNull();
    expect(grace.getByText("Grace Hopper")).toBeVisible();
    expect(grace.getByText("Never signed in")).toBeVisible();
    expect(grace.getByRole("button", { name: "Remove grace" })).toBeVisible();
  });

  it("says that only people add or remove people, and how safe accounts are", async () => {
    renderWith(<SettingsPanel />);

    expect(await screen.findByText(/Only people can add or remove people/)).toBeVisible();
    expect(screen.getByText(/Human accounts are a soft wall/)).toBeVisible();
  });

  it("asks before removing a person, then removes them", async () => {
    const user = userEvent.setup();
    const { api } = renderWith(<SettingsPanel />);
    const people = within(await region("People"));

    await user.click(await people.findByRole("button", { name: "Remove grace" }));
    expect(people.getByText("Remove grace? They can no longer sign in.")).toBeVisible();
    await user.click(people.getByRole("button", { name: "Remove for good" }));

    expect(api.calls).toContain("deleteHuman grace");
    expect(await people.findByText("grace was removed")).toBeVisible();
    expect(people.queryByText("Grace Hopper")).toBeNull();
  });

  it("can back out of removing a person", async () => {
    const user = userEvent.setup();
    const { api } = renderWith(<SettingsPanel />);
    const people = within(await region("People"));

    await user.click(await people.findByRole("button", { name: "Remove grace" }));
    await user.click(people.getByRole("button", { name: "Keep them" }));

    expect(people.getByRole("button", { name: "Remove grace" })).toBeVisible();
    expect(api.calls.filter((c) => c.startsWith("deleteHuman"))).toEqual([]);
  });

  it("shows the daemon's reason when a person cannot be removed", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.mutationError = problem(409, "Conflict", "Grace still owns a running task.");
    renderWith(<SettingsPanel />, api);
    const people = within(await region("People"));

    await user.click(await people.findByRole("button", { name: "Remove grace" }));
    await user.click(people.getByRole("button", { name: "Remove for good" }));

    expect(await people.findByText("Grace still owns a running task.")).toBeVisible();
  });

  it("adds a person", async () => {
    const user = userEvent.setup();
    const { api } = renderWith(<SettingsPanel />);
    const form = within(await screen.findByRole("form", { name: "Add a person" }));

    await user.type(form.getByLabelText("Name"), "linus");
    await user.type(form.getByLabelText("Display name"), "Linus Torvalds");
    await user.type(form.getByLabelText("Password"), GOOD_PASSWORD);
    await user.type(form.getByLabelText("Password again"), GOOD_PASSWORD);
    await user.click(form.getByRole("button", { name: "Add person" }));

    expect(api.calls).toContain("createHuman linus");
    expect(await screen.findByText("linus was added")).toBeVisible();
    expect(await screen.findByText("Linus Torvalds")).toBeVisible();
    expect(form.getByLabelText("Name")).toHaveValue("");
  });

  it("checks the new person's details before sending", async () => {
    const user = userEvent.setup();
    const { api } = renderWith(<SettingsPanel />);
    const form = within(await screen.findByRole("form", { name: "Add a person" }));

    await user.type(form.getByLabelText("Name"), "1bad");
    await user.type(form.getByLabelText("Password"), "short");
    await user.click(form.getByRole("button", { name: "Add person" }));

    const alert = await form.findByRole("alert");
    expect(alert).toHaveTextContent("The name must start with a letter");
    expect(alert).toHaveTextContent("Add a display name");
    expect(alert).toHaveTextContent("The password needs at least 12 characters.");
    expect(alert).toHaveTextContent("The two passwords are not the same.");
    expect(api.calls.filter((c) => c.startsWith("createHuman"))).toEqual([]);
  });

  it("shows why a person could not be added", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.mutationError = problem(409, "Conflict", "Someone named linus already exists.");
    renderWith(<SettingsPanel />, api);
    const form = within(await screen.findByRole("form", { name: "Add a person" }));

    await user.type(form.getByLabelText("Name"), "linus");
    await user.type(form.getByLabelText("Display name"), "Linus Torvalds");
    await user.type(form.getByLabelText("Password"), GOOD_PASSWORD);
    await user.type(form.getByLabelText("Password again"), GOOD_PASSWORD);
    await user.click(form.getByRole("button", { name: "Add person" }));

    expect(await form.findByText("Someone named linus already exists.")).toBeVisible();
  });

  it("copes with many people and long names", async () => {
    const api = new FakeApi();
    const [first] = fixtures.humans;
    if (!first) throw new Error("no fixture");
    const many: Human[] = Array.from({ length: 40 }, (_, i) => ({
      ...first,
      name: `person-${String(i)}`,
      display_name: i === 3 ? "Very ".repeat(60) + "long name" : `Person ${String(i)}`,
      last_login_at: i % 2 === 0 ? null : first.last_login_at,
    }));
    api.humans = many;
    const { container } = renderWith(<SettingsPanel />, api);

    const people = within(await region("People"));
    expect(await people.findAllByRole("button", { name: /^Remove person-/ })).toHaveLength(40);
    await expectNoAxeViolations(container);
  });

  it("explains a failure to load the people", async () => {
    const api = new FakeApi();
    api.humans = problem(503, "Unavailable", "The account store is starting.");
    renderWith(<SettingsPanel />, api);

    const people = within(await region("People"));
    expect(await people.findByRole("alert")).toHaveTextContent("The account store is starting.");
  });
});

describe("Settings, Claude engine", () => {
  it("shows who accepted the licence and how the download is going", async () => {
    renderWith(<SettingsPanel />, acceptedApi());

    const engine = within(await region("Claude engine"));
    expect(await engine.findByText(/Accepted by ada/)).toBeVisible();
    expect(engine.getByText(/Licence version 2026-10-01/)).toBeVisible();
    expect(await engine.findByText("Downloading now.")).toBeVisible();
    expect(engine.getByRole("progressbar", { name: "Engine download" })).toHaveAttribute(
      "value",
      "0.42",
    );
  });

  it("says when the engine is ready", async () => {
    const api = acceptedApi();
    api.engine = { ...fixtures.engineFetch, state: "ready", progress: 1, error: null };
    renderWith(<SettingsPanel />, api);

    const engine = within(await region("Claude engine"));
    expect(await engine.findByText("The engine is ready.")).toBeVisible();
    expect(engine.queryByRole("progressbar")).toBeNull();
  });

  it("shows a failed download with its reason", async () => {
    const api = acceptedApi();
    api.engine = {
      ...fixtures.engineFetch,
      state: "failed",
      error: "The download was cut off.",
    };
    renderWith(<SettingsPanel />, api);

    const engine = within(await region("Claude engine"));
    expect(await engine.findByText("The download failed.")).toBeVisible();
    expect(engine.getByRole("alert")).toHaveTextContent("The download was cut off.");
  });

  it("lets you read the licence text", async () => {
    const user = userEvent.setup();
    renderWith(<SettingsPanel />, acceptedApi());
    const engine = within(await region("Claude engine"));

    await user.click(await engine.findByText("Read the licence"));

    expect(engine.getByRole("heading", { name: "Claude Code licence" })).toBeVisible();
  });

  it("is off by default and offers nothing to accept until you turn it on", async () => {
    const { api } = renderWith(<SettingsPanel />);
    const engine = within(await region("Claude engine"));

    expect(await engine.findByText(/The Claude engine is off/)).toBeVisible();
    expect(engine.getByRole("button", { name: "Turn on the Claude engine" })).toBeVisible();
    expect(engine.queryByRole("button", { name: "Accept and download" })).toBeNull();
    expect(engine.queryByRole("region", { name: "Claude engine licence text" })).toBeNull();
    expect(engine.queryByRole("progressbar")).toBeNull();
    expect(api.calls.some((c) => c.startsWith("acceptEngineLicence"))).toBe(false);
  });

  it("turning it on shows the licence and the plain facts, then accepting starts the download", async () => {
    const user = userEvent.setup();
    const { api } = renderWith(<SettingsPanel />);
    const engine = within(await region("Claude engine"));

    await user.click(await engine.findByRole("button", { name: "Turn on the Claude engine" }));

    expect(engine.getByText(/Read the licence below/)).toHaveFocus();
    expect(engine.getByText("You use your own Anthropic account, key or plan.")).toBeVisible();
    expect(
      engine.getByText("Agents One does not provide, pay for or resell Claude usage."),
    ).toBeVisible();
    expect(
      engine.getByRole("link", { name: /Anthropic's legal and compliance terms/ }),
    ).toHaveAttribute("href", "https://code.claude.com/docs/en/legal-and-compliance");
    const text = engine.getByRole("region", { name: "Claude engine licence text" });
    expect(within(text).getByRole("heading", { name: "Claude Code licence" })).toBeVisible();
    expect(api.calls.some((c) => c.startsWith("acceptEngineLicence"))).toBe(false);

    await user.click(engine.getByRole("button", { name: "Accept and download" }));

    expect(api.calls).toContain("acceptEngineLicence 2026-10-01");
    expect(await engine.findByText(/Accepted by ada/)).toBeVisible();
    expect(engine.queryByRole("button", { name: "Accept and download" })).toBeNull();
  });

  it("cancel closes the licence without accepting it", async () => {
    const user = userEvent.setup();
    const { api } = renderWith(<SettingsPanel />);
    const engine = within(await region("Claude engine"));

    await user.click(await engine.findByRole("button", { name: "Turn on the Claude engine" }));
    await user.click(engine.getByRole("button", { name: "Cancel" }));

    expect(engine.queryByRole("button", { name: "Accept and download" })).toBeNull();
    expect(engine.getByRole("button", { name: "Turn on the Claude engine" })).toHaveFocus();
    expect(api.calls.some((c) => c.startsWith("acceptEngineLicence"))).toBe(false);
  });

  it("shows why the licence could not be accepted", async () => {
    const user = userEvent.setup();
    const api = new FakeApi();
    api.acceptEngineLicence = () =>
      Promise.reject(problem(502, "Bad gateway", "The licence server did not answer."));
    renderWith(<SettingsPanel />, api);
    const engine = within(await region("Claude engine"));

    await user.click(await engine.findByRole("button", { name: "Turn on the Claude engine" }));
    await user.click(engine.getByRole("button", { name: "Accept and download" }));

    expect(await engine.findByText("The licence server did not answer.")).toBeVisible();
  });

  it("keeps the plain facts in view once the engine is on", async () => {
    renderWith(<SettingsPanel />, acceptedApi());
    const engine = within(await region("Claude engine"));

    expect(
      await engine.findByText("You use your own Anthropic account, key or plan."),
    ).toBeVisible();
    expect(
      engine.getByRole("link", { name: /Anthropic's legal and compliance terms/ }),
    ).toBeVisible();
  });

  it("has no accessibility violations with the licence open", async () => {
    const user = userEvent.setup();
    const { container } = renderWith(<SettingsPanel />);
    const engine = within(await region("Claude engine"));

    await user.click(await engine.findByRole("button", { name: "Turn on the Claude engine" }));

    await expectNoAxeViolations(container);
  });

  it("explains a failure to load the licence", async () => {
    const api = new FakeApi();
    api.licence = problem(501, "Not implemented", "The engine is not set up on this machine.");
    renderWith(<SettingsPanel />, api);

    const engine = within(await region("Claude engine"));
    expect(await engine.findByRole("alert")).toHaveTextContent(
      "The engine is not set up on this machine.",
    );
  });
});

describe("Settings, the whole page", () => {
  it("says the control page holds the internet list, model keys, budget and Freeze all", async () => {
    renderWith(<SettingsPanel />);

    expect(
      await screen.findByText(
        /internet allow list, model keys, the total budget and Freeze all live on the control page/,
      ),
    ).toBeVisible();
  });

  it("has no accessibility violations", async () => {
    const { container } = renderWith(<SettingsPanel />, acceptedApi());
    await screen.findByText(/Accepted by ada/);
    await screen.findByText("Grace Hopper");

    await expectNoAxeViolations(container);
  });
});
