<!-- SPDX-License-Identifier: MIT -->

# Guide for builders: plugins and runners

This guide is for you if you want to add to Agents One: a skill, an agent template, an MCP server,
a theme, or a runner that thinks for agents in a new way. You write files and, for a runner, a
small program. You do not change the daemon.

> **Not finished yet.** Agents do not run on the machine yet: the daemon does not start runners
> today. The plugin format, the runner protocol, the Go runner library and the conformance kit are
> on `main` and tested. You can build against them now, and you will see your work run once the
> daemon starts agents.

## What you can build

A **plugin** is a folder with a manifest, `agentsone.plugin.json`, and the files it lists. One
plugin can carry any mix of seven kinds of part.

| Part | What it is | State |
| --- | --- | --- |
| Skill | A folder with a `SKILL.md` that tells agents how to do something. | Built |
| Agent template | A JSON file that describes an agent a human or another agent can hire. | Built |
| MCP server | Tools for agents, over stdio or Streamable HTTP. | Built |
| Runner | A program that thinks for one agent and speaks the runner protocol. | Built |
| Theme | Colours and type for the interface. | Built |
| Panel | An interface panel in a sandboxed frame. | Coming |
| Event source | A program that publishes events, such as webhooks, files or git. | Coming |

"Built" means the code and tests are on `main`. The manifest already has places for panels and
event sources, so you can write them, but nothing loads them yet.

## The manifest

Every plugin has `agentsone.plugin.json` at its root. The schema is
[`plugin-manifest.schema.json`](../../contract/schemas/plugin-manifest.schema.json); add its
address as `$schema` and your editor checks the file as you type. The smallest real example is
[`plugins/agents-one-theme`](../../plugins/agents-one-theme/agentsone.plugin.json). A fuller one is
[`plugins/small-seed`](../../plugins/small-seed/agentsone.plugin.json).

```json
{
  "name": "my-plugin",
  "version": "0.1.0",
  "description": "One short line that says what it adds.",
  "author": { "name": "Your name" },
  "license": "MIT",
  "source": { "kind": "agentsone", "ref": "https://github.com/you/my-plugin" },
  "compat": { "agentsone": ">=0.1.0 <1.0.0" },
  "contents": {
    "mcpServers": [],
    "skills": [{ "name": "my-skill", "path": "./skills/my-skill", "description": "When to use it." }],
    "agentTemplates": [],
    "runners": [],
    "panels": [],
    "themes": [],
    "emitters": []
  }
}
```

Rules to remember:

- All seven lists must be present, empty if unused. A plugin whose lists are all empty is refused.
- The version must be a semantic version, such as `0.1.0`.
- Every path is inside the plugin and starts with `./` or `${PLUGIN_ROOT}/`. A path that leaves
  the plugin, or a segment that starts with a dot, is refused. `${PLUGIN_ROOT}` also works inside
  `command`, `args`, `env` and `url` values.
- Part names are lowercase letters, digits and dashes, starting with a letter, up to 64 characters.
- `compat.agentsone` says which Agents One versions the plugin works with. A plugin with runners
  also lists `runnerProtocol`, for example `["1.1", "1.0"]`.

Agents One also reads three foreign formats and writes this manifest for you: Claude Code plugins,
Agent Skills and the MCP registry. If you already have one of those, you may not need to write a
manifest at all. [`contract/schemas/plugins/SOURCES.md`](../../contract/schemas/plugins/SOURCES.md)
lists what maps and what is left out. Parts that cannot be mapped are named in the install report.
They are never dropped silently.

## Skills

A skill is a folder with a `SKILL.md`. Its frontmatter holds a `name` that equals the folder name,
and a `description` that says when to use it. The body is plain instructions. See
[`hire-an-agent`](../../plugins/small-seed/skills/hire-an-agent/SKILL.md). List it in
`contents.skills` with the same name, path and description.

## Agent templates

An agent template is a JSON file, checked against
[`agent-template.schema.json`](../../contract/schemas/agent-template.schema.json). It holds the
agent's `name`, `kind` (`always-on`, `chat` or `waker`), whether it is root, which runner it uses
and that runner's config, its one-line status, its charter and goals, its skills, MCP servers,
resource limits, the events that wake it and its schedules. List the file in
`contents.agentTemplates`. The three templates in
[`plugins/small-seed/agents`](../../plugins/small-seed/agents) are good starting points.

Name a runner by its manifest name, for example `"runner": { "name": "openai", "config": {} }`.

## MCP servers

A stdio server names a `command`, its `args` and `env`. The daemon starts it as a child process.
A remote server names a `url` and uses the `streamable-http` transport. Its host must be on the
internet allow list that a human keeps on the control page, because agents cannot reach anything
else. Put no secret in a header. Use a `${user_config.KEY}` value, which the daemon fills in when
it starts the server.

## Themes

A theme is a JSON file checked against
[`theme.schema.json`](../../contract/schemas/theme.schema.json). List it in `contents.themes`.
[`plugins/agents-one-theme`](../../plugins/agents-one-theme) is the default theme. A theme carries a light and a
dark set, and lists the colour pairs that must meet WCAG 2.2 AA contrast. CI fails a theme whose
ratios are too low. People pick a theme in **Settings**.

## Build a runner

A runner is the program that thinks for one agent. The daemon starts one runner process per agent
and talks to it over a Unix socket. The path is in the environment variable
`AGENTSONE_RUNNER_SOCKET`. The messages are JSON-RPC 2.0, one per line. Tools never travel over
this socket. The runner reaches the machine through the MCP endpoint in its welcome, and reaches
a model through the model gateway.

The full rules are in the [runner protocol guide](../../contract/schemas/runner/PROTOCOL.md). The
exact message shapes are in
[`messages.schema.json`](../../contract/schemas/runner/messages.schema.json). Read the guide once
before you start. The short version:

1. The runner connects and sends `hello` within 10 seconds, with the protocol versions it speaks.
2. The daemon answers with the **welcome**: the agent's identity, the MCP endpoint, the model
   gateway (model name `autoagents/one`) and tokens, the session summary and settings.
3. The runner sends `status` `idle`. Then the daemon may `wake` it, with a reason: `message`,
   `task`, `event`, `timer` or `chat`.
4. During a turn the runner sends `output`, `usage` and other notifications. **Every turn must send
   at least one `profile.update`**: a line of at most 255 characters that says the agent's goal and
   what it is doing now. The router reads these lines.
5. It ends the turn with `status` `idle`. It also sends a `heartbeat` every 15 seconds, answers
   `cancel`, `sleep` and `shutdown`, and pauses a turn when the budget runs out.

### Use the Go library

You do not have to write all of that. [`runners/sdk`](../../runners/sdk) does the handshake,
framing, heartbeat, turn life cycle, budget pause and token renewal. You write the part that is
yours: a `Handler` with three methods.

| Method | What it does |
| --- | --- |
| `Start(ctx, welcome)` | Runs once after the welcome. Keep the session summary here. |
| `Wake(ctx, turn, wake)` | Runs one turn. Return `sdk.ErrBudgetExhausted` to pause it, or `nil` when done. |
| `Sleep(ctx, request)` | Compacts the session into a Markdown summary within the size limit. |

Inside `Wake` you talk to the daemon only through the `*sdk.Turn`. It numbers output chunks and
keeps the rules for you. Its methods include `ProfileUpdate`, `Output`, `FinishOutput`, `Usage`,
`BusSend`, `TaskDone` and `Log`. A runner that can resume a paused turn also implements `Resumer`.

The echo runner is the smallest working example, about 200 lines with comments:
[`runners/echo`](../../runners/echo). Its `main` is five lines:

```go
func main() {
	os.Exit(sdk.Main(echo.Options(), echo.New()))
}
```

[`runners/openai`](../../runners/openai) is a full runner that calls a model through the gateway.

Three rules that matter:

- **Tokens.** Read the gateway and MCP tokens from `Welcome.Credentials` right before each call,
  never from a copy. On protocol 1.1 the daemon renews them while turns run. Never log a token,
  store it in memory records or send it on the bus. The SDK prints tokens as `[redacted]`.
- **One turn at a time.** A wake that arrives during a turn is queued.
- **Budget.** When the gateway refuses a call for budget, pause the turn and resume it where it
  stopped when the budget returns. If you cannot resume, say so in your capabilities.

You can write a runner in another language. Speak the protocol directly, and use the conformance
kit below to check it.

### Test it with the conformance kit

[`runners/conformance`](../../runners/conformance) plays the daemon against your runner and checks
one rule per test. Each test names the rule id from the protocol guide, such as F1 or H2. Describe
your runner and call `Run` from a test:

```go
func TestConformance(t *testing.T) {
	conformance.Run(t, conformance.Subject{
		Name:    "my-runner",
		Start:   conformance.Binary("/path/to/my-runner"), // or conformance.InProcess(run)
		Prompts: conformance.Prompts{Plain: "hello", Hold: "hold", Budget: "budget"},
	})
}
```

`Prompts` are the texts that make your runner do what a rule needs: a short turn, a turn that stays
open until cancelled, and a turn whose model call is refused for budget. Every line your runner
sends is also checked against the schema, the size limit and the request id order while any rule
runs. `conformance.ReplayTranscript` plays a recorded session from
[`contract/fixtures/schemas/runner-message`](../../contract/fixtures/schemas/runner-message) and
compares your lines with it.

Run the tests with `go test ./runners/...`.

### Ship it in a plugin

Add the runner to `contents.runners`:

```json
{
  "name": "my-runner",
  "description": "What it thinks with.",
  "command": "${PLUGIN_ROOT}/bin/my-runner",
  "args": [],
  "env": {},
  "protocolVersions": ["1.1", "1.0"]
}
```

Then an agent template can use it with `"runner": { "name": "my-runner", "config": {} }`. The
`config` object arrives in the welcome as `config.runner`.

Binaries must match the machine's CPU. List `compat.arch` (`amd64`, `arm64`) if your plugin ships
binaries for only some.

## Install and try a plugin

Install from the **Plugins** panel, or through the daemon API (`installPlugin`). Humans and agents
may install, and every install is written to the audit trail. The install report lists anything
that was left out. To remove a plugin, uninstall it. The daemon removes everything it registered.

## Where to look next

- [Runner protocol guide](../../contract/schemas/runner/PROTOCOL.md): every message, error code
  and conformance rule.
- [Contract](../../contract/README.md): the frozen API and the rules for changing it.
- [Architecture](../architecture.md): why a root agent cannot get around the hard walls, which
  matters when you decide what a plugin may ask for.
- [Contributing](../../CONTRIBUTING.md): send your work back to the project.
