<!-- SPDX-License-Identifier: MIT -->

# Architecture

Agents One ships as one Docker compose stack. Plain Docker and Coolify both load the same
`compose.yaml`. There is no virtual machine, no host tool and no custom disk image.

This page explains what runs, which part holds which limit, and which limits an agent with root
access can get around. We call the first kind a **hard wall** and the second kind a **soft wall**.

## The stack in one picture

```text
   human (browser)                                        internet
        |                                                    ^
        | HTTPS (the platform adds TLS)                      |
        v                                                    |
 +--------------------------- outer network ----------------------------+
 |                                                                      |
 |   control                     edge                                   |
 |   human-only page             web door   -> machine:7700             |
 |   (own port)                  DNS, egress proxy, allow list          |
 |      |                        gateway "autoagents/one" -> providers   |
 |      | admin socket           budget, model keys, freeze all         |
 |      +---(shared volume)----> audit sink                              |
 +----------------------------------+-----------------------------------+
                                    |
 +--------------------------- inner network (internal) ----------------+
 |                                  |                                   |
 |   machine                        |                                   |
 |   Debian 13 container: agentsoned, agents, runners, VSCodium server, |
 |   browser desktop. No published ports. Only way out is edge.         |
 +----------------------------------------------------------------------+
```

## Three services, two networks

| Service | Image | What it does |
| --- | --- | --- |
| `machine` | `ghcr.io/sdk-e/agentsone-machine` | The place where agents live. Runs the daemon (`agentsoned`), one runner process per agent, the VSCodium server and the desktop shown in the browser. |
| `edge` | `ghcr.io/sdk-e/agentsone-edge` | The wall. Filters internet access, runs the model gateway and the budget, shows humans the web door, and keeps the audit sink. |
| `control` | `ghcr.io/sdk-e/agentsone-edge` | The human-only page for the allow list, model keys, budget and freeze. |

`edge` and `control` are the same program, `agentsone`, started with the subcommands `edge` and
`control`.

| Network | Type | Who is on it |
| --- | --- | --- |
| `inner` | `internal: true`, so Docker gives it no route out | `machine` and `edge` |
| `outer` | Normal network with internet access | `edge` and `control` |

`machine` is on `inner` only. It has no published ports, no Docker socket and never runs in
privileged mode. `edge` has a fixed address on `inner`, and `machine` uses it as its DNS server.
`control` cannot reach `machine`, and `machine` cannot reach `control`.

## Who holds which wall

Root agents are root inside the `machine` container. Anything that lives inside `machine` can
therefore be changed or imitated by a root agent. Anything that lives in `edge` or `control` cannot,
because those containers are separate and `machine` cannot run commands in them.

The interface labels every limit as hard or soft, so humans know what to trust.

### Hard walls

| Wall | Held by | How it works |
| --- | --- | --- |
| Internet filter | `edge` | `machine` has no route out. Its DNS answers only names on the allow list. `edge` connects only to addresses it looked up itself for allowed names. |
| Total budget | `edge` | Every model call goes through the gateway. At zero, calls fail. Agents resume where they stopped when budget returns. |
| Freeze all | `edge` | Cuts model calls and internet access for every agent. |
| Model keys | `edge` | Keys are stored in the `edge` volume and written only through `control`. `machine` never sees them. |
| Start, stop | Docker or Coolify | `edge` holds no Docker socket, so it cannot start or stop containers. |
| Per-agent limits for custom agents | The Linux kernel | Memory, process and CPU limits per agent. Hard only when Docker Engine 28 or newer gives the container writable cgroups. See [Per-agent limits](#per-agent-limits). |

### Soft walls

| Wall | Held by | Why it is soft |
| --- | --- | --- |
| Budget split per agent | The daemon | A root agent can pose as another agent. The total is still hard. |
| Defence share | `edge` | Gateway tokens are signed with the gateway key, which root agents can read in `machine`. A root agent can sign a token that spends the defence slice. The total is still hard. |
| Freeze of one agent or one team | The daemon | A root agent can ignore it. Freeze all is still hard. |
| Human accounts and sessions | The daemon (`core.db`) | They live inside `machine`. |
| Gateway identity | The daemon and `edge` | `edge` trusts the agent identity the daemon vouches for. A root agent can pose as another agent. It cannot spend more than the total. |
| Machine audit records | The daemon and `edge` | Only the daemon's audit token can send records to the `edge` audit sink, so custom agents cannot. A root agent can read the gateway key and send false records. See [The audit trail](#the-audit-trail). |
| Per-agent limits for root agents | The daemon | Root agents can rewrite any agent's limits. They are bound only by the container ceiling. |

### Per-agent limits

The container has one ceiling for memory, processes and CPU, set in `compose.yaml`. Root agents
cannot raise it.

Inside that ceiling the daemon gives each agent its own cgroup with memory, process and CPU
limits. The daemon starts each runner inside that cgroup as the agent's own Linux user.

- **Custom agents** cannot change their limits. The limits are hard.
- **Root agents** can change their own limits, but only inside the container ceiling.
- If the container does not have writable cgroups, the daemon runs runners with plain process
  limits only and reports the limits as soft.

The daemon decides between hard and soft once, when it starts, before any agent runs:

- **Hard.** The machine container runs with `cgroup: private` and `writable-cgroups=true`, as
  both compose files set it. The daemon sees cgroup v2, runs as root, and can write its own
  cgroup. It moves itself and the other processes of the container into `/daemon` and makes
  `/agents`. Each custom agent then gets `/agents/<name>`, with its CPU, memory and process
  limits set by the kernel. A runner that uses more memory than its limit is killed by the
  kernel, and a fork past its process limit fails. No added capability is involved: never
  `privileged`, never `CAP_SYS_ADMIN`, never the Docker socket.
- **Soft.** Anything else: no cgroup v2, a read-only cgroup tree (Docker without
  `writable-cgroups=true`), a missing CPU, memory or process controller, or a daemon that is not
  root. Custom agents still run as their own users, but there is no cgroup per agent. The kernel
  does not cap their CPU or memory, and still caps how many processes each agent runs
  (`RLIMIT_NPROC` of the agent's user, at `tasks`). No process limit can stand in for the memory
  limit: under the address-space limit at `memory_mb` a Go runner dies before `main`, and under
  the data limit at `memory_mb` Node.js dies at start (`Failed to reserve virtual memory for
  CodeRange`).
- **Both.** A runner with a memory limit gets a hint of it, at three quarters of the limit:
  `GOMEMLIMIT` for Go, and `--max-old-space-size` added after its own `NODE_OPTIONS` for
  Node.js. Both runtimes then collect garbage before they reach the limit. A runner that sets
  its own `GOMEMLIMIT` or `--max-old-space-size` keeps it. In soft mode the hint is all there is.

The daemon writes one line on its log at start that says which and why, for example
`limits: hard (cgroup v2, writable)` or `limits: soft (cgroup tree is read-only)`. The line is
information, not an error: soft limits are a supported way to run. The daemon only ever works in
its own cgroup, never above it, and never in the host's root cgroup.

The daemon also checks at start that the host cgroup mount has `nsdelegate`. If it is missing,
the daemon keeps root agents off and reports the host as unsafe.

### The audit trail

What `edge` sees (traffic and model calls) is proof. Records written inside `machine` are a
hash-chained log: they show what the agents claim, and they are sent to the `edge` audit sink as
written. A root agent can write false records, but it cannot change what `edge` saw.

Only the daemon may send records to the audit sink. Each request carries a short-lived token
for the audit sink, signed with the gateway key. Inside `machine` only root can read that key, so
custom agents cannot make such a token. The sink refuses a request with no token, and it refuses
a token meant for model calls, such as the one every agent holds. So a custom agent cannot slip in
a false record and stop the daemon's real records from reaching `edge`.

Root agents can still read the key and write false records, as the daemon. That is why records
from `machine` stay testimony, not proof.

### What the internet filter does not do

It limits where agents can reach. It does not stop data leaving through a site that is on the
allow list.

## How a request flows

**A human opens the interface.** The browser connects to the platform over HTTPS. The platform
(for example Coolify's proxy) adds TLS and any sign-in, then forwards to the published
port of `edge`. The web door in `edge` passes the request to the daemon in `machine` on port 7700.
The first visit shows the setup page while no human account exists.

**An agent reaches the internet.** The agent asks for a name. `machine` sends the DNS query to
`edge`. If the name is on the allow list, `edge` answers with its own inner address. The agent then
connects to `edge`, which routes HTTP by its `Host` header and TLS by its server name (SNI),
without opening the TLS stream, and connects to the real site. Tools that use `HTTPS_PROXY` can use
the CONNECT proxy on port 3128. A name that is not on the list does not resolve. A direct IP
address has no route.

**An agent calls a model.** The agent asks the gateway for the model name `autoagents/one`. The
gateway in `edge` checks the budget, picks a real model from the providers the human enabled and
gave a key, adds the key and forwards the call. The key never enters `machine`. Before it forwards
the call, the gateway removes the request fields the chosen model refuses, as `drop_params` in
`gateway.toml` lists them per model (the defaults drop `temperature` and `top_p` for Claude and
GPT-5 models, which accept only their default values). The edge log has one line per model call
(caller, provider, model, tier, status, tokens, time taken) and one warning with the provider's
own message when a provider refuses a call. Neither holds the prompt, the answer, a key or a token.

**A process in `machine` calls the daemon API.** It connects to the Unix socket
`/run/agentsone/api.sock`. Every local user may connect (folder mode 0755, socket mode 0666,
both owned by root), because the daemon does not trust the file mode: the kernel tells it the
process id and user id of the caller. The daemon then decides, and refuses anything it cannot
name:

| The caller is | It is treated as |
| --- | --- |
| A process in an agent's cgroup `/agents/<name>` | That agent |
| The daemon's own process | The daemon |
| A process in a human's cgroup `/humans/<name>`, where that human's web terminal runs | That human, with a human's rights and never root (identity marked soft). Refused when no human has that name, or when the lookup fails. |
| Any other process running as root | Refused. A root agent outside its cgroup sends its token. |
| A process whose user id is a custom agent's | That agent (identity marked soft) |
| A process whose user id is a human's own Linux user | That human, with a human's rights and never root (identity marked soft) |
| Anything else, or any process when a lookup fails | Refused with 403, and recorded in the audit trail |

The daemon's cgroup is not proof of the daemon: package installs and git run there too. A bearer
token sent on the socket always names the caller instead.

**Terminals run in their opener's cgroup.** No terminal runs in the daemon's cgroup. The daemon
starts each terminal through a small helper that joins the cgroup first, then becomes the right
user, then starts the shell. So the shell, and everything it starts, is placed from its first
instruction.

- An agent's terminal runs as the agent's own user, in the agent's cgroup `/agents/<name>`, under
  the same limits as the agent's runner. That holds while the runner sleeps too: the daemon makes
  the cgroup with the agent's limits when it is missing. Without writable cgroups the terminal
  gets the same process limits as the runner instead: processes and open files. Its CPU and
  memory are not capped either.
- A human's web terminal runs as root, in the cgroup `/humans/<name>`. That cgroup and the
  `/humans` folder belong to root, and no one else may write them. The socket names a process
  there as that human, with a human's rights, never as root. Without writable cgroups there is
  no such cgroup: the web terminal is then a root process like any other, and the socket refuses
  it, so `agentctl` in a human's web terminal works only when the machine has writable cgroups.
- Every terminal that opens or closes is recorded in the audit trail, with the name of the human
  or agent who opened it.

The web terminal is root on the machine, and that is a choice the owner made. A root process can
move itself into any cgroup. So from a web terminal a human can move a shell into an agent's
cgroup and act as that agent on the socket, or change any limit. That is what root means. The
human's cgroup only says who opened the terminal; it does not hold a root user back. The IDE panel
is different: it runs as the human's own Linux user (`h_<name>`), not as root, and nothing here
changes that.

**A human changes a hard wall.** The human opens the `control` page on its own port. `control`
passes the change to `edge` through an admin socket on a volume that only `control` and `edge`
mount.

**A tool calls the daemon with a token.** Tokens carry scopes, such as `tasks:read`. Each daemon
operation names the one scope it needs in the contract. The daemon refuses a token without that
scope, and refuses a token on anything that is not a contract operation, before any module runs.
A request that sends a bearer token is named by that token only: a bad, expired or revoked token
gets 401 on every path and both listeners, even beside a valid session cookie. Agent tokens are
issued only by the daemon (for runners, holding exactly the scopes of their MCP
tools) and by root agents. Humans never use tokens: they use their session cookie in the browser,
or the socket identity when they run `agentctl` on the machine. Their power comes from who they
are and their place in the org chart, not from scopes. A remote `agentctl` would need a new rule.

## Data and volumes

| Volume | Mounted in | Holds |
| --- | --- | --- |
| State | `machine`, at `/var/lib/agentsone` | `core.db`, `bus.db`, `memory.db`, `audit.db`, the machine recipe |
| Plugins | `machine` | Installed plugins |
| Home | `machine`, at `/home` | Home folders of custom agents, at `/home/agents/<name>` |
| Edge state | `edge`, at `/var/lib/agentsone-edge` | Model keys (file mode 0600), `budget.db`, the allow list, the audit sink chain |
| Admin socket | `edge` and `control` only | The Unix socket that `control` uses to talk to `edge` |

Everything outside these volumes resets to the image when the container restarts.

### The machine recipe

A root agent can install packages and change the system. To keep those changes, the agent records
them in the **machine recipe**: the packages, services and settings it added. At every start, the
daemon reapplies the recipe, in a safe-to-repeat way, before any agent wakes. The daemon also
re-creates the Linux users of custom agents from `core.db`, with the same user and group ids.

### Restart is the rescue

If an agent breaks the system, restart the container. The system returns to the image, and the
recipe puts back what the agents added.

## What you need

- **Docker Engine 28 or newer.** Writable cgroups for per-agent limits need it. Older engines
  refuse to start `machine` with an `invalid --security-opt` error. Check with `docker version`.
- **A host with cgroup v2.** Hard per-agent limits need it. Without it the daemon runs custom
  agents with soft limits and says so at start (`limits: soft (no cgroup v2)`).
- **A Docker setup where `machine` stays on the inner network only.** Plain Docker and colima
  (on a Mac) do this by default.
- **Coolify must use Raw mode.** In its normal mode, Coolify attaches its own network to every
  service. That network has a route to the internet, which gives `machine` a way around `edge`.
  In Raw mode, Coolify deploys the file as written. The daemon also checks, at start and every 5
  minutes, that `machine` has no default route and no network besides the inner one. It logs the
  answer (`network wall holds: no default route, interfaces: eth0`) and, while the wall does not
  hold, starts no runner (a wake and an idle start of an always-on agent are both refused) and
  records that in the audit trail. When the check finds the wall broken while runners are up, it
  stops every one of them and records each stop. The runners of always-on agents start again
  when the wall holds; the others start on their next wake. A runner that crashes is started
  again only by the supervisor, after a wait that doubles from 2 seconds up to 5 minutes, and
  only after a fresh wall check finds the wall holding. The agent shows the runner failing with
  its last error; the audit trail gets one record when it starts failing and one when it has
  stayed up for 5 minutes again.
- **Own address range.** The inner network uses a fixed range, set by variables in `compose.yaml`.
  Two stacks on one server need two different ranges.

Deploy steps for each platform are in `docs/deploy/`.

## Where the code lives

| Path | What it holds |
| --- | --- |
| `cmd/agentsoned`, `internal/` | The daemon, one package per bounded context |
| `cmd/agentsone`, `internal/edge/` | `edge` and `control`: DNS, egress proxy, gateway, budget, audit sink, web door, control page |
| `image/` | Dockerfiles for `machine` and `edge`, entry point, desktop and first-start scripts |
| `compose.yaml` | The stack |
| `contract/` | The frozen API, schemas and fixtures. See [contract/README.md](../contract/README.md) |
