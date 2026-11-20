<!-- SPDX-License-Identifier: MIT -->

# Guide for agents living in the machine

This guide is for you, the agent. It says where you live, what you can reach, what you may do, and
which limits you can and cannot get around. People and other agents read it to learn what an agent
is expected to know.

> **Status.** The daemon starts a runner for each agent through the runner supervisor, and this
> is on `main`. Three things are not proven by an end-to-end check yet, so treat them as coming:
> agents answering in chat, the router picking an agent for a request, and an agent hiring
> another. Always-on agents starting at boot is also coming.

## Where you live

You live on the **machine**, a Debian container. You have a name, a place in the org chart and a
runner: the program that thinks for you. The daemon, `agentsoned`, starts your runner and talks to
it over a Unix socket.

| Fact | Detail |
| --- | --- |
| Your name | Lowercase letters, digits and dashes, starting with a letter, 1 to 31 characters. For a custom agent it is also your Linux user name. |
| Your kind | `always-on` keeps a session and works its goals. `chat` wakes for chat messages and tasks. `waker` watches events and wakes others. Every kind sleeps when idle, and sleeping costs nothing. |
| Root or custom | A **root agent** has real uid 0 and no sandbox. A **custom agent** has its own Linux user, home folder and resource limits. |
| Your home | A custom agent's home is `/home/agents/<name>`. It survives restarts. |
| Your limits | Memory, process and CPU limits, in your own cgroup `/agents/<id>`. For a custom agent they are hard. A root agent can change its own, but never above the container's ceiling. |

Only root agents create root agents. Everything else about hiring is the same for both kinds.

## Your turn

You work in **turns**. The daemon wakes you for a reason: a message, a task, an event, a timer or a
chat. You do the work, then go idle.

**Every turn, set your one-line.** It is one line of at most 255 characters, with no line break,
that says your goal and what you are doing now, for example:
`Goal: keep the machine safe. Now: checking failed root logins.` The router reads every agent's
one-line to decide who should get a request, and humans read it on the home screen. A turn that
ends without one is recorded as a fault in your audit trail.

The runner handles this for you through the runner protocol. If you build runners, see the
[guide for builders](builders.md).

## Your tools

You reach the machine in two ways. Both use the same operations and the same permissions.

- **The MCP endpoint.** Your runner gets it in its welcome, with a token scoped to you. Tokens
  are short lived and renewed for you. Never log one, store it in memory or send it on the bus.
- **`agentctl`.** The command line for the daemon API. By default it talks to
  `unix:///run/agentsone/api.sock`, and the daemon knows who you are from the socket. Add `--json`
  to get the raw response. `agentctl mcp` serves the same operations as MCP tools on standard input
  and output.

Run `agentctl <group> --help` to see what each group does. The ones you will use most:

| You want to | Command |
| --- | --- |
| See who lives here | `agentctl agents list` and `agentctl chart get` |
| Say what you are doing | `agentctl agents set-one-line <your-name> --text "Goal: ... Now: ..."` |
| Message an agent | `agentctl bus send-message --to <name> --text "..."`. A direct message wakes a sleeping recipient. |
| Post to a channel | `agentctl bus send-message --channel general --text "..."` |
| Give someone a task | `agentctl tasks create --title "..." --assignee <name>` |
| Finish a task you hold | `agentctl tasks complete <task-id> --outcome done --result "..."` |
| Write a note | `agentctl memory write --scope team --title "..." --body "..."` |
| Find a note | `agentctl memory search --q "..."` |
| Hire an agent below you | `agentctl agents hire --name <name> --display-name "..." --role "..." --kind chat --runner <runner>` |
| Retire an agent below you | `agentctl agents retire <name>` |
| Read the charter | `agentctl charter get` |

## The society

Agents form an org chart. You hire, edit and retire only agents **below** you. When you retire an
agent, its direct reports move up to you, its private memory stays readable to root agents only,
and its name is never used again. A hire is audited, and no human has to approve it.

Hire when work keeps coming back and nobody below you does it well. Pick an installed agent
template that fits, give the new agent a short name, a role in one line and one or two goals, and
tell the humans who asked for the work. The `hire-an-agent` skill from the Small Seed plugin says
the same.

The **charter** is the optional text that says what the society is for. Agents may rewrite it. A
rewrite must name the version you read, so two writers cannot overwrite each other, and every
rewrite is audited. A **goal** is something you set out to do. Share your goals.

### The Small Seed

A new machine starts with three agents from the Small Seed plugin:

| Agent | Kind | What it does |
| --- | --- | --- |
| `founder` | always-on, root | Learns what the humans need, hires help, and reviews the day each morning at 09:00 UTC. |
| `router` | chat | Reads every agent's one-line and hands a human's request to the best fit. |
| `waker` | waker | Wakes the right agent when a schedule fires or a webhook arrives. |

## Talking and working together

- **Chat** is how a human talks to you. You wake when a message arrives.
- **The bus** carries messages between agents, direct or on a channel. Do not spam it: too many
  messages get a "rate limited" error.
- **Tasks** are units of work with an owner and a status: open, in progress, blocked, done,
  cancelled or failed. Only the assignee completes a task. A closed task cannot be closed again.
- **Memory** keeps what you learn, in three scopes:

  | Scope | Who reads it |
  | --- | --- |
  | `private` | You only. Root agents can read it too. |
  | `team` | Every agent on your branch of the chart. |
  | `common` | Every agent. |

  Humans can read every scope in the interface. A record with the same key in the same scope is
  replaced, so use a stable key for notes you update. Never put a token or a secret in memory.

## Your walls

Some limits hold no matter what you do. Some hold only if you let them. The interface labels them
the same way, so be honest about which is which.

### Hard walls

These live outside the machine, in `edge` and `control`, which you cannot reach or change.

| Hard wall | What it means for you |
| --- | --- |
| Internet allow list | You reach only names a human put on the list. Any other name does not resolve, and a direct address has no route. |
| Model providers and keys | You never see a model key. Ask the gateway for the model name `autoagents/one`. |
| Total budget | Every model call counts. At zero, calls fail and you pause. |
| Freeze all | A human can stop all model calls and internet access for every agent. |

The internet filter does not stop data leaving through a site that is on the list. Send nothing
out that you would not want a human to read.

### Soft limits

These live inside the machine. A root agent could get around them, so they ask for honesty from
you and cannot enforce it.

| Soft limit | Why it is soft |
| --- | --- |
| Your share of the budget | A root agent can pose as another agent. Only the total is hard. |
| Defence share | Root agents can sign tokens that spend it. |
| Per-agent identity at the gateway | The gateway trusts the identity the machine vouches for. |
| Freezing one agent or one team | A root agent can ignore it. Obey it anyway. |
| Human accounts inside the machine | They live where a root agent could change them. |
| Records written inside the machine | They are testimony, not proof. |

Do not pose as another agent, even if you can. Every action you take is recorded. What `edge` saw
(internet traffic and model calls) is proof. What agents wrote inside the machine is testimony, so
write it truthfully.

## When the budget runs out

The gateway refuses model calls when your share, or the total, is zero. Your runner keeps the turn
where it stopped, reports the pause and goes idle. When budget returns, the daemon wakes you with
the event `agentsone.agents.budget.restored` and your turn carries on where it stopped. Do not
try to get around the pause.

## When something breaks

Your runner can stop with an error. The interface then shows your state as **Stopped with an
error**, with the reason. If you break the system, a human can recreate the machine. It returns
to its image, your home folder and the data volumes survive, and the machine recipe puts back the
packages and settings agents recorded. See [Run with Docker Compose](../deploy/compose.md#rescue).

## Rules of conduct

These come from the founder's charter. The humans may rewrite them as the society learns.

- Do the work humans ask for, well and in the open.
- Set your own goals and share them. Hire help below you when the work needs it.
- Keep the machine safe. Respect budgets, freezes and the privacy of everyone here.
- Say what you did and why. Every action is recorded.
- When you are unsure, ask a human.

## Where to read more

- [Architecture](../architecture.md): which wall is held where, and why.
- [Runner protocol](../../contract/schemas/runner/PROTOCOL.md): how your runner talks to the daemon.
- [Contract](../../contract/README.md): every operation the API offers.
