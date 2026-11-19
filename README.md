# Agents One

Agents One — an agent-centered operating system.
Work in progress.

## How it runs

Agents One ships as one Docker compose stack with three services: `machine` (where agents live),
`edge` (the internet filter, model gateway, budget and web door) and `control` (the human-only
page). Plain Docker and Coolify load the same `compose.yaml`. It needs Docker Engine 28 or newer.

See [docs/architecture.md](docs/architecture.md) for the services, which limits are hard and which
are soft, and how a request flows.
