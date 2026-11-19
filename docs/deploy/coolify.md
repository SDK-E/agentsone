# Deploy Agents One on Coolify

Agents One runs on [Coolify](https://coolify.io) from the same `compose.yaml` as everywhere else,
plus a small overlay, [`compose.coolify.yaml`](../../compose.coolify.yaml), that adds domains and
TLS through Coolify's Traefik proxy.

## Use Raw mode, always

Coolify has two ways to deploy a compose file. Only **Raw mode** ("Raw (deploy file as-is)")
keeps the machine's network wall.

- In the normal mode, Coolify adds its own network to every service. That network has a gateway,
  so the machine gets a default route and direct internet, around every filter in the edge.
- In Raw mode, Coolify adds only its labels and leaves the networks alone. The machine stays on
  the inner network only.

If you see the machine reach the internet without the edge, the deployment is not in Raw mode.

## What you need

- Docker Engine 28.0 or newer on the server. The Coolify installer accepts Docker 24 and newer,
  but the machine container does not start on Docker older than 28.0. Check with
  `docker version` on the server before you deploy.
- Two domains pointing at the server: one for the web door, one for the control page.

## Make the file Coolify needs

Coolify deploys one compose file. Merge the stack and the overlay into one file, keeping every
`${...}` setting so Coolify shows each as an editable variable:

```sh
docker compose -f compose.yaml -f compose.coolify.yaml config --no-interpolate > agentsone-coolify.yaml
```

## Deploy

1. In Coolify, create a new resource of type **Docker Compose Empty** and choose **Raw** mode.
2. Paste the contents of `agentsone-coolify.yaml`.
3. Set the variables:
   - `AGENTSONE_DOMAIN`: the web door domain, for example `agents.example.org`.
   - `AGENTSONE_CONTROL_DOMAIN`: the control page domain.
   - Any setting from [compose.md](compose.md#settings) you want to change.
4. Deploy. Coolify's Traefik serves both domains over HTTPS with certificates from Let's Encrypt
   (resolver `letsencrypt`, entry points `http` and `https`).
5. Put a sign-in in front of the control page on the platform side. It holds the human-only
   powers: the allow list, model keys and the budget.

## How the overlay works

- Only `edge` and `control` join Coolify's `coolify` network, where Traefik runs. The machine
  never joins it.
- The web door listens on two addresses: the edge's outer address (for the published port) and
  the alias `agentsone-door` on the `coolify` network (for Traefik). It never listens on the
  inner address. Change the alias with `AGENTSONE_PROXY_ALIAS` if another app on the server
  already uses it.
- Traefik router names start with `agentsone-`. Run one Agents One stack per Coolify server, or
  rename the routers in the merged file.

## Not yet tested on a live Coolify server

The overlay follows the Coolify v4 source and the Traefik 3.7 label format, but it has not been
deployed on a live Coolify server yet. The first deployment should check that the machine has no
default route (`docker exec <machine container> cat /proc/net/route` shows no `00000000`
destination) and that both domains answer.
