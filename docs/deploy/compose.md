# Run Agents One with Docker Compose

Agents One is one Docker Compose stack, [`compose.yaml`](../../compose.yaml) at the root of the
repository. It runs the same way on a laptop and on a server.

## What you need

- **Docker Engine 28.0 or newer.** The machine container uses writable cgroups to give each agent
  its own limits, and older engines refuse to start it with `invalid --security-opt`. Docker
  26.1.3 or newer is also needed for the machine's DNS questions to reach the edge on an internal
  network. Check with `docker version --format '{{.Server.Version}}'`.
- **Docker Compose v2 or newer.** Tested with Compose 5.8.0.
- **A Linux host with cgroup v2.** Docker Desktop, colima and current Linux servers have it.
- On a Mac, colima works well: `brew install colima docker docker-compose`, then
  `colima start --cpu 4 --memory 8`.

## Start it

```sh
docker compose up -d --wait
```

Then open the web door at <http://127.0.0.1:7780> and the control page at
<http://127.0.0.1:7781>. Both listen on loopback only until you change `AGENTSONE_BIND_ADDR`.
Put TLS and a sign-in in front before you open them to a network.

Stop it with `docker compose down`. Your data stays in named volumes; `docker compose down -v`
deletes it.

## Versions

`compose.yaml` pins the images to the release in the [`VERSION`](../../VERSION) file, so the
same file always starts the same build. Set `AGENTSONE_IMAGE_TAG` to pick another one:

| Tag | What it is | Use it for |
| --- | --- | --- |
| `v0.1.0` | One release. Never changes. | Running Agents One. This is the default. |
| `v0.1` | The newest patch release of `0.1`. Moves with each patch release. | Staying on a minor version without editing the tag. |
| `main` | The newest build of `main` that passed the end-to-end checks. Moves with every merge. | The development stack. |
| `sha-<commit>` | The build of one commit. Never changes. | Testing one commit, as the end-to-end run does. |

There is no `latest` tag.

Until `v0.1.0` is released, the default tag does not exist yet. Set `AGENTSONE_IMAGE_TAG=main`
in your environment or in a `.env` file next to `compose.yaml`:

```sh
AGENTSONE_IMAGE_TAG=main docker compose up -d --wait
```

A release gives the already tested `sha-<commit>` images their version tags and does not rebuild
them, see [Releasing](../../CONTRIBUTING.md#releasing).

## The three services

| Service | Image | What it does |
| --- | --- | --- |
| `machine` | `ghcr.io/sdk-e/agentsone-machine` | Debian container where the agents live. On the inner network only. |
| `edge` | `ghcr.io/sdk-e/agentsone-edge` | Internet filter, model gateway, budget, web door. The only way out of the machine. |
| `control` | `ghcr.io/sdk-e/agentsone-edge` | The human-only control page. Not reachable from the machine. |

## The walls

- `machine` is only on `inner`, an internal network with no gateway. It has no route to the
  internet and no published ports.
- The machine's DNS server is the edge. The edge answers only names on the allow list, and
  passes traffic only to those names.
- The edge web door listens on the outer network only, never on the inner one.
- `control` is only on `outer`. It talks to the edge through a socket on a volume that the
  machine never mounts.
- The edge's data volume (model keys, allow list, budget) is mounted by the edge only. The one
  volume the edge shares with the machine holds the token signing key, and the machine mounts it
  read-only.
- `edge` and `control` run as non-root users with no Linux capabilities and a read-only image.
- No container is privileged and none mounts the Docker socket.
- Never start any container on the host with `cgroup: host` (`--cgroupns=host`). It turns off
  cgroup delegation protection for the whole host, and then root inside the machine could raise
  its own memory limit.

## Settings

Every setting has a default. Change one with an environment variable or a `.env` file next to
`compose.yaml`.

| Variable | Default | Meaning |
| --- | --- | --- |
| `AGENTSONE_IMAGE_TAG` | `v0.1.0` | Image tag for all three services. The default is the release in the [`VERSION`](../../VERSION) file. See [Versions](#versions). |
| `AGENTSONE_BIND_ADDR` | `127.0.0.1` | Host address the web door and control page listen on. |
| `AGENTSONE_WEB_DOOR_PORT` | `7780` | Host port of the web door. |
| `AGENTSONE_CONTROL_PORT` | `7781` | Host port of the control page. |
| `AGENTSONE_MACHINE_CPUS` | `4` | CPU ceiling of the machine. Must not exceed the host's CPUs. |
| `AGENTSONE_MACHINE_MEMORY` | `8g` | Memory ceiling of the machine, with no swap. |
| `AGENTSONE_MACHINE_PIDS` | `4096` | Process ceiling of the machine. |
| `AGENTSONE_EDGE_CPUS` | `2` | CPU ceiling of the edge. |
| `AGENTSONE_EDGE_MEMORY` | `1g` | Memory ceiling of the edge. |
| `AGENTSONE_INNER_SUBNET` | `10.77.42.0/27` | Subnet of the inner network. |
| `AGENTSONE_INNER_DYNAMIC_RANGE` | `10.77.42.16/28` | Part of the inner subnet Docker hands out to the machine. |
| `AGENTSONE_EDGE_IP` | `10.77.42.2` | Edge address on the inner network, outside the dynamic range. |
| `AGENTSONE_OUTER_SUBNET` | `10.77.42.32/27` | Subnet of the outer network. |
| `AGENTSONE_OUTER_DYNAMIC_RANGE` | `10.77.42.48/28` | Part of the outer subnet Docker hands out to control. |
| `AGENTSONE_EDGE_OUTER_IP` | `10.77.42.34` | Edge address on the outer network, where the web door listens. |

The machine ceiling is a hard wall set by Docker outside the container. Root agents may change
their own limits inside it, never above it. Custom agents are held to their per-agent limits.

The default subnets avoid the ranges Docker, Coolify, Tailscale, Kubernetes and Podman use. If
Docker reports `Pool overlaps with other one on this address space`, pick another free `/27` for
each network and move the edge addresses with it. Two Agents One stacks on one host need two
different sets of subnets.

## Rescue

The machine goes back to its image whenever its container is recreated. That is the rescue: if
something inside the machine is broken, for example by a root agent, recreate it:

```sh
docker compose up -d --force-recreate machine
```

What survives: the six volumes the machine mounts.

| Volume | Path in the machine | Holds |
| --- | --- | --- |
| `machine-state` | `/var/lib/agentsone` | The daemon's databases and the machine recipe |
| `machine-plugins` | `/var/lib/agentsone/plugins` | Installed plugins |
| `machine-audit` | `/var/lib/agentsone/audit` | Audit records |
| `machine-home` | `/home` | The agents' home folders |
| `machine-engines` | `/var/lib/agentsone-engines` | The Claude engine, downloaded once after a human accepts its licence; every agent can run it, none can change it |
| `gateway-key` | `/run/agentsone/gateway-key` | The token signing key, read-only, owned by the edge |

What resets: everything else in the machine goes back to the image, such as packages added with
`apt`, files in `/etc`, `/opt`, `/usr`, `/root` and `/tmp`, and users added by hand. At start the
daemon re-creates the agents' users and reapplies the machine recipe. Recreating the machine does
not touch `edge` or `control`.

A plain restart does not reset anything: when the machine crashes and Docker restarts it
(`restart: unless-stopped`), or after `docker restart` or `docker compose restart`, the container
layer is kept. That is harmless: the machine's start-up and its recipe reapply idempotently at
every start. Only a recreate resets. The end-to-end check `e2e/run.sh rescue` proves both.

## Deploy on Coolify

See [coolify.md](coolify.md). Coolify must use Raw mode.
