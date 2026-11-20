# Contributing to Agents One

Thank you for helping. This page is the short version. The fuller [guide for contributors](docs/guides/contributors.md) walks through a change from
clone to merge.

## Repository layout

```text
contract/                 schemas, OpenAPI, fixtures
cmd/                      agentsoned, agentctl, agentsone
internal/                 daemon packages (api, auth, core, store, agents, bus, ...)
internal/edge/            edge and control packages (net, gateway, budget, audit, door, control)
runners/                  sdk, echo, openai, claude
packages/contract-ts/     generated TypeScript types
ui/                       shell, theme system, home, panels, panel plugin host
plugins/                  small-seed, agents-one-theme, sample-mcp
emitters/                 auth, services, disk-load, files, git, clock, webhook
image/                    Dockerfiles for machine and edge, entry point, desktop, first start
compose.yaml              the stack: machine, edge, control
brand/                    mark, colours, type, README art
docs/                     architecture.md, deploy/, guides/ (human, builder, agent, contributor)
e2e/  .github/workflows/
```

## Tools

You need Go (see `go.mod` for the version), Node 24 or newer, and pnpm (the version is pinned in
`package.json`). Install web dependencies with `pnpm install`.

## Commands

| Command          | What it does                                                         |
| ---------------- | -------------------------------------------------------------------- |
| `make fmt`       | Format Go and web files in place                                     |
| `make lint`      | Run golangci-lint and ESLint                                         |
| `make test`      | Run Go tests with the race detector, and web tests                   |
| `make gen`       | Regenerate code from the contract (runs `scripts/gen.sh` when there) |
| `make check-gen` | Fail when generated code is out of date                              |
| `make check`     | Narrow local checks: SPDX, `gofmt`, `go vet`, format check, types    |

Run heavy commands through `scripts/heavy`, for example `scripts/heavy go test -race ./...`. It
limits how many heavy jobs run at once on one machine (`AGENTSONE_HEAVY_SLOTS`, default 4).

## Rules

- **Conventional commits.** Use `feat:`, `fix:`, `docs:`, `chore:`, `refactor:`, `test:`, `ci:`
  and so on. Pull request titles follow the same format.
- **SPDX header.** Every source file starts with `SPDX-License-Identifier: MIT` in a comment
  within its first 5 lines. `scripts/check-spdx.sh` enforces it. JSON files are exempt.
- **Zero warnings.** No warnings or errors from lint, types, tests, build, logs or CI. A flaky
  test counts as a failure.
- **Latest releases.** Use the latest release of every dependency, tool and action. Pin actions
  by full commit SHA.
- **Never bypass hooks.** Do not use `--no-verify`.

## Code design

The daemon is split into bounded contexts, one package each under `internal/`, named in the
words of the glossary below. The contract and the seams between contexts are frozen; see
[contract/FREEZE.md](contract/FREEZE.md).

### The shape of a context

Every context has the same layout. `internal/audit` is the reference; copy it.

```text
internal/<context>/
  doc.go          what the context is, in domain words, and which work package owns it
  module.go       wiring only: New(core.Deps) *Module, Name, Provide, Migrate, API, Start, Stop
  module_test.go
  domain/         entities, value objects that check their own rules, domain errors and
                  repository interfaces; no IO and no generated contract types
  app/            use cases; depend on domain and on core ports only
  adapters/http   implements the generated <tag>api server (embed <tag>api.Unimplemented)
                  and maps contract types to and from domain types in one place
  adapters/sqlite repositories on internal/store, with embedded migrations
  adapters/memory in-memory repositories for tests
  <context>test/  the repository contract suite, run on every repository implementation
```

- A context reaches another one only through a port in `core.Ports`, filled by its owner in
  `Provide`. Never import another context's package.
- Generated contract types stay in `adapters/http`. They never reach `domain` or `app`.
- A context keeps its own settings table and reads it with `deps.Config.Section`.

### Clean code

- Small functions with names that say what they do. No dead code.
- Comments say why, not what the code already says.
- Wrap every error with context: `fmt.Errorf("hire %s: %w", name, err)`.
- Pass dependencies in through constructors. No package-level variables, except fixed lookup
  tables and generated registries, each with a comment saying why.

### Test-driven development

Write the failing test first, then the code that makes it pass, for every behaviour. Use
hand-written fakes from `internal/core/coretest` and the context's own test package; mocking
libraries are not allowed. [TESTING.md](TESTING.md) has the full rules and the coverage floors.

### Glossary

| Word | Meaning |
| --- | --- |
| agent | A resident of the machine: a runner process with a name, a place in the chart and goals. |
| root agent | An agent with real uid 0 and no sandbox. Only root agents create root agents. |
| custom agent | An agent with its own Linux user, home and cgroup limits. |
| agent kind | How an agent lives: always-on, chat or waker. |
| waker | An event-driven agent that watches events and wakes others. |
| chart | The org chart of agents. An agent hires, edits and retires only below itself. |
| team | One branch of the chart, named after the agent at its head. |
| charter | The optional text that states what the society is for. Agents may rewrite it. |
| goal | Something an agent set out to do. |
| one-line | The line under 255 characters that every agent turn emits: its goal and what it does. |
| live list | Every agent's current one-line. The router reads it. |
| router | The agent that matches a human's request to the best agent and opens its chat. |
| bus | Messages between agents, direct or on channels. |
| event | A CloudEvents 1.0 envelope with sequence, actor, chart scope, causation and correlation. |
| emitter | A source of events, such as auth, services, disk, files, git or webhooks. |
| trigger | A rule that turns a matching event into an action, such as a wake or a task. |
| schedule | A timer that wakes an agent or creates a task. |
| task | A unit of work assigned to an agent, with a status and a result. |
| memory scope | Who may read a memory record: private, team or common. Root agents read all. |
| plugin | An installable bundle: MCP servers, skills, agent templates, runners, panels or themes. |
| runner | The program that runs one agent and speaks the runner protocol to the daemon. |
| budget wall | A spending limit. Hard walls live in `edge`; soft walls inside `machine`. |
| defence slice | Budget reserved for wakers and security work. It can be switched off. |
| freeze | A lever that stops all agents, one team or one agent, until released. Freeze all is held by `edge`; the others are soft. |
| rescue | Restarting the container. The system returns to the image and the machine recipe reapplies what agents added. |
| door | The web door: how humans reach `machine`, through `edge`. |
| edge | The service that holds the human-only walls: internet filter, model gateway, budget, audit sink. |
| control | The service with the human-only page. It reaches `edge` through an admin socket. |

## Pull request flow

1. Branch from `main`.
2. Make a focused change with tests.
3. Run `make check` and the narrow checks that match your change.
4. Open a pull request with a conventional title and fill in the template.
5. CI must pass. The `gate` check is the required one.

## Releasing

Images are built once, on `main` (`images.yml`), and tested as `sha-<commit>`. A release gives that
tested build its version tags and does not rebuild it. Maintainers release like this:

1. Set the new version in the [`VERSION`](VERSION) file (plain `X.Y.Z`) and change the default
   image tag in `compose.yaml` and `compose.coolify.yaml` to `v<VERSION>`. CI checks that they
   agree (`scripts/check-version.sh`). Merge that pull request.
2. Wait until `images.yml` is green for the merge commit: the images are built and the
   end-to-end run passed.
3. Push the tag `vX.Y.Z` on that commit.

The `release` workflow then refuses to go on unless the tag is `v<VERSION>`, the commit is on
`main` and every `images.yml` job passed for it. It tags the images `vX.Y.Z` and `vX.Y` (never
`latest`, and an existing `vX.Y.Z` is never moved), writes the changelog from the conventional
commit titles with [git-cliff](https://git-cliff.org), and publishes a GitHub release with the
compose files, the image SBOMs, `SHA256SUMS` and a build provenance attestation.

Security problems go through private reporting, see [SECURITY.md](SECURITY.md). By taking part
you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).
