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
  `docker version` on the server before you deploy.
- Two domains pointing at the server: one for the web door, one for the control page.

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
   - `AGENTSONE_IMAGE_TAG`: leave it unset to run the release pinned in the file. Until `v0.1.0`
     is released, set it to `main`. See [Versions](compose.md#versions).
   - Any setting from [compose.md](compose.md#settings) you want to change.
5. Deploy. Coolify's Traefik serves both domains over HTTPS with certificates from Let's Encrypt
   (resolver `letsencrypt`, entry points `http` and `https`).
6. Put a sign-in in front of the control page on the platform side. It holds the human-only
   powers: the allow list, model keys and the budget.

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

- Only `edge` and `control` join Coolify's `coolify` network, where Traefik runs. The machine
  never joins it.
- The web door listens on two addresses: the edge's outer address (for the published port) and
  the alias `agentsone-door` on the `coolify` network (for Traefik). It never listens on the
  inner address. Change the alias with `AGENTSONE_PROXY_ALIAS` if another app on the server
  already uses it.
- Traefik router names start with `agentsone-`. Run one Agents One stack per Coolify server.
- After a change to `compose.yaml` or the overlay, remake `compose.coolify.yaml` with the command
  in its header. The pull request check fails until you do.

## Not yet tested on a live Coolify server

The file and the steps follow the Coolify v4.7.6 source and the Traefik 3.7 label format, but
they have not been tried on a live Coolify server yet. The first deployment should check that the
machine has no default route (`docker exec <machine container> cat /proc/net/route` shows no
`00000000` destination) and that both domains answer.
