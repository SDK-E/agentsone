# Deploy Agents One on Coolify

Agents One runs on [Coolify](https://coolify.io) from
[`compose.coolify.yaml`](../../compose.coolify.yaml): the same stack as
[`compose.yaml`](../../compose.yaml), plus domains and TLS through Coolify's Traefik proxy. The
Coolify part lives in [`compose.coolify-overlay.yaml`](../../compose.coolify-overlay.yaml), and CI
checks that the deployable file matches the two.

## Raw mode, always

Coolify can deploy a compose file in two ways. Only **Raw mode** keeps the machine's network wall.

- In the normal mode ("Managed by Coolify"), Coolify adds its own network to every service. That
  network has a gateway, so the machine gets a default route and direct internet, around every
  filter in the edge.
- In Raw mode ("Raw (deploy file as-is)"), Coolify adds only its labels and leaves the networks
  alone. The machine stays on the inner network only.

Raw mode exists only for an **application** with the Docker Compose build pack. A
"Docker Compose Empty" service has no Raw mode, so do not deploy Agents One that way.

If the machine reaches the internet without the edge, the deployment is not in Raw mode.

## What you need

- Docker Engine 28.0 or newer on the server. The Coolify installer accepts Docker 24 and newer,
  but the machine container does not start on Docker older than 28.0. Check with
  `docker version --format '{{.Server.Version}}'` on the server before you deploy.
- Docker Compose v2 or newer (Coolify's installer brings it).
- A Linux server with cgroup v2. Check with `stat -fc %T /sys/fs/cgroup`: it must print
  `cgroup2fs`. Then each custom agent gets hard limits, and the machine's log says
  `limits: hard (cgroup v2, writable)` at start. Without it the log says `limits: soft` and why.
- Two domains pointing at the server: one for the web door, one for the control page.
- A server that is not shared with another Agents One stack: the subnets and router names are
  fixed per stack.

## Deploy

1. In a Coolify project, add a new resource from a **Public Repository**:
   `https://github.com/SDK-E/agentsone`, branch `main`.
2. Choose the **Docker Compose** build pack, base directory `/`, and Docker Compose location
   `/compose.coolify.yaml`.
3. Before the first deployment, open **Advanced** and set **Compose deployment** to
   **Raw (deploy file as-is)**. Leave **Predefined network** off.
4. Set the environment variables:
   - `AGENTSONE_DOMAIN`: the web door domain, for example `agents.example.org`.
   - `AGENTSONE_CONTROL_DOMAIN`: the control page domain.
   - `AGENTSONE_IMAGE_TAG`: leave it unset to run the build pinned in the file (see
     [Versions](#versions)).
   - Any setting from [Settings](#settings) you want to change.
5. Deploy. Coolify's Traefik serves both domains over HTTPS with certificates from Let's Encrypt
   (resolver `letsencrypt`, entry points `http` and `https`).
6. Put your platform's access control in front of both domains, and most of all the control page.
   It holds the human-only powers: the allow list, model keys and the budget. The file does not
   add any sign-in of its own in front of the web door; the control page has its own passphrase
   (see [First run](#first-run)), which is a second lock, not a replacement. If your access
   control is a Traefik middleware (sign-in, IP allow list), name it in
   `AGENTSONE_DOOR_MIDDLEWARES` and `AGENTSONE_CONTROL_MIDDLEWARES` before the first deployment,
   so neither page is ever served without it.

## Versions

There is no release yet, so `compose.coolify.yaml` pins one tested build of `main` by its commit
tag, `sha-<commit>`. That build passed the end-to-end run, and the tag never changes, so every
deployment of this file starts the same images. There is no `latest` tag. The tag appears in the
default of `AGENTSONE_IMAGE_TAG` on all three services, so one setting moves them together.

To move to another build, set `AGENTSONE_IMAGE_TAG` in Coolify to the new tag and redeploy:

- `sha-<full 40-character commit>` for one build of `main` that passed the end-to-end run. The
  `images` workflow publishes it only after that run, for both images.
- `main` for the newest tested build of `main`. It moves with every merge, so a redeploy can pull
  a different build.
- `v<version>`, once a release exists. Then the pin in the file can follow `compose.yaml` again.

See [Versions in compose.md](compose.md#versions) for the full tag list. Always move all three
services together: the setting does that.

## Ports

The Coolify file publishes no port on the host. Traefik reaches the two pages over the proxy
network, so its route, with your access control in front, is the only way in.

| Service | Container port | Published on the host | Who reaches it |
| --- | --- | --- | --- |
| `edge` | 8080 (web door) | Not published | Traefik, for `AGENTSONE_DOMAIN`. Humans, through your platform's access control. |
| `control` | 8081 (control page) | Not published | Traefik, for `AGENTSONE_CONTROL_DOMAIN`. Humans only. |
| `edge` | 53, 80, 443, 3128, 4000, 7710 | Not published | The machine, on the inner address only. |
| `machine` | none | Not published | Nobody. Humans reach agents through the web door. |

Keep these private:

- Do not add host ports to `edge` or `control`: a host port would be a second way in that skips
  your access control.
- Do not publish any port of `machine`, and do not add a network to it.
- Do not give the control page the same access as the web door. Agents' users and humans who may
  chat with agents are not the people who set the budget and the model keys.

## Settings

Every setting has a default except the two domains. Set them in Coolify under **Environment
Variables**. None is a secret: the model keys, the allow list and the budget are not set here,
they live behind the control page.

| Variable | Required | Default | Purpose |
| --- | --- | --- | --- |
| `AGENTSONE_DOMAIN` | Yes | none | Domain of the web door. The deployment fails to start without it. |
| `AGENTSONE_CONTROL_DOMAIN` | Yes | none | Domain of the control page. The deployment fails to start without it. |
| `AGENTSONE_IMAGE_TAG` | No | the pinned `sha-` tag in the file | Image tag of all three services. See [Versions](#versions). |
| `AGENTSONE_PROXY_NETWORK` | No | `coolify` | Docker network where Traefik reaches edge and control. Set it when your server runs apps on another network than Coolify's own. |
| `AGENTSONE_DOOR_MIDDLEWARES` | No | empty | Traefik middlewares in front of the web door, comma-separated, for example `sign-in@file,vpn-only@file`. Empty adds none. |
| `AGENTSONE_CONTROL_MIDDLEWARES` | No | empty | Traefik middlewares in front of the control page, in the same form. Set it unless something else already guards the control domain. |
| `AGENTSONE_PROXY_ALIAS` | No | `agentsone-door` | Name of the web door on the proxy network. Change it if another app on the server uses it. |
| `AGENTSONE_MACHINE_CPUS` | No | `4` | CPU ceiling of the machine. Must not exceed the server's CPUs. |
| `AGENTSONE_MACHINE_MEMORY` | No | `8g` | Memory ceiling of the machine, with no swap. |
| `AGENTSONE_MACHINE_PIDS` | No | `4096` | Process ceiling of the machine. |
| `AGENTSONE_EDGE_CPUS` | No | `2` | CPU ceiling of the edge. |
| `AGENTSONE_EDGE_MEMORY` | No | `1g` | Memory ceiling of the edge. |
| `AGENTSONE_INNER_SUBNET` | No | `10.77.42.0/27` | Subnet of the inner network. Change it if Docker reports an overlap, or if your proxy or firewall trusts that range. |
| `AGENTSONE_INNER_DYNAMIC_RANGE` | No | `10.77.42.16/28` | Part of the inner subnet handed to the machine. |
| `AGENTSONE_EDGE_IP` | No | `10.77.42.2` | Edge address on the inner network, outside the dynamic range. The machine uses it as its DNS server and its one trusted proxy. |
| `AGENTSONE_EGRESS_DENY` | No | empty | Extra destination ranges the machine never reaches, even through a name on the allow list. Comma-separated CIDR ranges or single addresses, IPv4 or IPv6. Set it to the server's own public addresses when your proxy trusts the proxy network (see below). The edge refuses to start when an item is not a range. |

`AGENTSONE_PROXY_NETWORK` and the two middleware settings change only the proxy network and the
Traefik labels. Plain `compose.yaml` has no proxy, so they exist only in the Coolify file.

The Coolify file has no outer network, so `AGENTSONE_OUTER_SUBNET`,
`AGENTSONE_OUTER_DYNAMIC_RANGE` and `AGENTSONE_EDGE_OUTER_IP` apply to plain `compose.yaml` only.
It publishes no host port either, so `AGENTSONE_BIND_ADDR`, `AGENTSONE_WEB_DOOR_PORT` and
`AGENTSONE_CONTROL_PORT` apply to plain `compose.yaml` only. Setting any of them in Coolify
changes nothing; see [How the Coolify file works](#how-the-coolify-file-works).

The edge reaches the internet through the proxy network. If your proxy trusts that network, for
example to let internal or VPN-only hosts through, an allowed name that resolves to the server's
own public address would come back to the proxy from a trusted address. Set
`AGENTSONE_EGRESS_DENY` to the server's public addresses (every IPv4 and IPv6 address the proxy
listens on) before you allow any name that points at the server. Requests to those addresses are
then refused, with the reason in the edge log and the edge audit log, and the edge start line
lists the ranges in force (`egress_deny=`).

The edge and control containers read no other environment variables; their addresses come from
the command line in the file. The machine reads `AGENTSONE_EDGE_IP` only. The same settings,
with the same meaning, are in [compose.md](compose.md#settings).

What you set on the control page after the first run, never in Coolify:

| Value | Required | Where it lives |
| --- | --- | --- |
| Control page passphrase | Yes | Set once with the setup code from the log; stored hashed in the `edge-data` volume. |
| Model keys | Yes, to run agents | Written only through the control page; stored in `edge-data`. The machine never sees them. |
| Internet allow list | Optional | Control page. Empty means agents reach no site. |
| Total budget | Optional | Control page. At zero, model calls fail. |

## Volumes

All volumes are named volumes with the `agentsone_` prefix and survive a Restart or a Redeploy.
Check Coolify's delete options before you remove the application: removing its volumes deletes
your keys, budget and the agents' files.

| Volume | Mounted by | Path in the container | Holds |
| --- | --- | --- | --- |
| `edge-data` | `edge` | `/var/lib/agentsone-edge` | Model keys, allow list, budget, control passphrase hash. Back this one up. |
| `gateway-key` | `edge` (write), `machine` (read-only) | `/var/lib/agentsone-edge/gateway-key`, `/run/agentsone/gateway-key` | The key that signs agents' model tokens. |
| `edge-admin` | `edge`, `control` | `/run/agentsone-edge-admin` | The admin socket between control and edge. Never mounted by the machine. |
| `machine-state` | `machine` | `/var/lib/agentsone` | The daemon's databases and the machine recipe. Back this one up. |
| `machine-plugins` | `machine` | `/var/lib/agentsone/plugins` | Installed plugins. |
| `machine-audit` | `machine` | `/var/lib/agentsone/audit` | Audit records. |
| `machine-home` | `machine` | `/home` | The agents' home folders and their work. Back this one up. |
| `machine-engines` | `machine` | `/var/lib/agentsone-engines` | The Claude engine, downloaded once after a human accepts its licence. |

## Health checks

Each service has a health check in the file, so Coolify shows its state and waits for it.

| Service | Check | Healthy when |
| --- | --- | --- |
| `edge` | `probe tcp <edge inner address>:53` | The edge's DNS answers on the inner network. |
| `control` | `probe tcp 127.0.0.1:8081` | The control page listens. It starts after `edge` is healthy. |
| `machine` | The image's own check | The daemon is ready. It starts after `edge` is healthy. |

Each runs every 30 seconds, after a start period of 30 seconds (60 for the machine). Do not add a Coolify health check on
the domains: the images have no shell or curl, so use the checks above.

## First run

1. Deploy, and wait until the three services are healthy.
2. Open the logs of the `control` service in Coolify and find the line
   `control page: no passphrase is set; open the control page and enter this setup code`,
   with `setup_code=...` after it. Only someone who can read the logs can do this.
3. Open the control page at `https://<AGENTSONE_CONTROL_DOMAIN>`, enter the setup code, and
   choose a passphrase. The code works once. Restarting the control service before you set a
   passphrase prints a new code.
4. On the control page, add the model keys, the internet allow list and the budget.
5. Open the web door at `https://<AGENTSONE_DOMAIN>`.
6. Check the wall in the logs of the `machine` service. At start, and again whenever the answer
   changes, the daemon logs `network wall holds: no default route, interfaces: eth0`. If it logs
   `network wall broken: no agent will wake` instead, the line says what breaks the wall and how
   to fix it. No runner starts until the wall holds again, and runners that were up are stopped
   (each stop is in the audit trail); the daemon checks every 5 minutes. When the wall holds
   again, the runners of always-on agents start by themselves.

## Upgrade

1. Pick the new tag (see [Versions](#versions)).
2. In Coolify, set `AGENTSONE_IMAGE_TAG` to it and deploy. If you use the pin in the file,
   pull the new revision of the repository instead and deploy.
3. Wait until the three services are healthy.

The volumes keep your keys, budget, agents and files. Recreating the machine resets everything
outside the volumes, as described in [Rescue](#rescue). To go back, set the tag to the old value
and deploy again.

## Rescue

The machine goes back to its image whenever its container is recreated; that is the rescue.

On Coolify, **Restart recreates every container of the stack.** For an application with the
Docker Compose build pack, Coolify turns a restart into a full deployment, removes the old
containers and runs `docker compose up -d`, which creates new ones. Redeploy does the same. The
volumes stay. Source, Coolify v4.7.6:

- `app/Livewire/Project/Application/Heading.php`, `restart()`: queues a deployment marked
  "restart only".
- `app/Jobs/ApplicationDeploymentJob.php`, constructor: "restart only" is turned off for the
  `dockercompose` build pack, so a restart runs a regular deployment.
- Same file, `deploy_docker_compose_buildpack()`: calls `stop_running_container(force: true)`,
  which stops and removes the old containers, then starts the stack with
  `docker compose ... up -d` (Raw mode branch).

So a Restart in Coolify resets the machine, and also edge and control, whose state lives in
volumes. What survives and what resets is the same as with plain Docker: see
[the rescue section of compose.md](compose.md#rescue).

When a container crashes, Docker restarts it on its own (`restart: unless-stopped`) and keeps its
container layer. That is harmless: the machine's start-up and its recipe reapply idempotently
at every start.

## How the Coolify file works

- Only `edge` and `control` join the proxy network where Traefik runs: Coolify's `coolify`
  network, or the one named in `AGENTSONE_PROXY_NETWORK`. The machine never joins it.
- Edge and control reach the internet through the proxy network. Unlike `compose.yaml`, the
  Coolify file has no separate outer network: `edge` is on the inner network and the proxy
  network, `control` on the proxy network only, and `machine` on the inner network only. So
  edge and control get no address range of their own, and a host firewall rule that limits what
  the proxy network may reach on the server covers them too.
- The web door listens on one address: the alias `agentsone-door` on the proxy network, for
  Traefik. It never listens on the inner address. Change the alias with `AGENTSONE_PROXY_ALIAS`
  if another app on the server already uses it.
- `edge` and `control` publish no host port (`ports: !reset []` in the overlay), unlike
  `compose.yaml`, where the loopback ports are the way in when no proxy runs.
- Traefik router names start with `agentsone-`. Run one Agents One stack per Coolify server.
- After a change to `compose.yaml` or the overlay, remake `compose.coolify.yaml` with the command
  in its header. The pull request check fails until you do.

## Not yet tested on a live Coolify server

The file and the steps follow the Coolify v4.7.6 source and the Traefik 3.7 label format, but
they have not been tried on a live Coolify server yet. The first deployment should check that the
machine has no default route (`docker exec <machine container> cat /proc/net/route` shows no
`00000000` destination) and that both domains answer.
