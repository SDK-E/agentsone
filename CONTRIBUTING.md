# Contributing to Agents One

Thank you for helping. This page is the short version. Fuller guides will live in `docs/guides/`.

## Repository layout

```text
contract/                 schemas, OpenAPI, fixtures
cmd/                      agentsoned, agentctl, agentsone
internal/                 daemon packages (api, auth, core, store, agents, bus, ...)
internal/host/            host packages (vm, net, gateway, budget, audit, door, control)
runners/                  sdk, echo, openai, claude
packages/contract-ts/     generated TypeScript types
ui/                       shell, theme system, home, panels, panel plugin host
plugins/                  small-seed, agents-one-theme, sample-mcp
emitters/                 auth, services, disk-load, files, git, clock, webhook
image/                    mkosi configuration, units, kiosk, first boot, boot splash
brand/                    mark, colours, type, splash, README art
docs/guides/              human, builder, agent, contributor
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

## Pull request flow

1. Branch from `main`.
2. Make a focused change with tests.
3. Run `make check` and the narrow checks that match your change.
4. Open a pull request with a conventional title and fill in the template.
5. CI must pass. The `gate` check is the required one.

Security problems go through private reporting, see [SECURITY.md](SECURITY.md). By taking part
you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).
