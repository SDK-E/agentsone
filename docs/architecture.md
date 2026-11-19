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

The daemon also checks at start that the host cgroup mount has `nsdelegate`. If it is missing,
the daemon keeps root agents off and reports the host as unsafe.

### The audit trail

What `edge` sees (traffic and model calls) is proof. Records written inside `machine` are a
hash-chained log: they show what the agents claim, and they are sent to the `edge` audit sink as
written. A root agent can write false records, but it cannot change what `edge` saw.

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
gateway in `edge` checks the budget, picks a real model from the providers the human enabled, adds
the key and forwards the call. The key never enters `machine`.

**A human changes a hard wall.** The human opens the `control` page on its own port. `control`
passes the change to `edge` through an admin socket on a volume that only `control` and `edge`
mount.

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
- **A Docker setup where `machine` stays on the inner network only.** Plain Docker and colima
  (on a Mac) do this by default.
- **Coolify must use Raw mode.** In its normal mode, Coolify attaches its own network to every
  service. That network has a route to the internet, which gives `machine` a way around `edge`.
  In Raw mode, Coolify deploys the file as written. The daemon also checks at start that `machine`
  has no default route, and refuses to start agents if it has one.
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
