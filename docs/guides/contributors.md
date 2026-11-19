<!-- SPDX-License-Identifier: MIT -->

# Guide for contributors

This guide takes you from a fresh clone to a merged pull request. It explains how the repository
fits together and how to make a change that passes review the first time.

Two shorter pages sit beside it. [CONTRIBUTING.md](../../CONTRIBUTING.md) holds the rules and the
glossary. [TESTING.md](../../TESTING.md) holds the testing standard. This guide points to them
instead of repeating them.

## Set up

You need:

- **Go**, at the version in `go.mod`.
- **Node 24 or newer** and **pnpm**, at the version pinned in `package.json`.
- **Docker Engine 28.0 or newer**, only if you run the whole stack. Most work does not need it.

```sh
git clone https://github.com/SDK-E/agentsone.git
cd agentsone
pnpm install
make check
```

`make check` runs the narrow local checks: the SPDX headers, `gofmt`, `go vet`, the format check
and the type check. If it passes on a clean clone, your setup is fine.

## Find your way around

| Path | What it holds |
| --- | --- |
| `contract/` | The frozen API, schemas, runner protocol and fixtures. |
| `internal/` | The daemon, one package per bounded context. |
| `internal/edge/` | `edge` and `control`: the internet filter, model gateway, budget, audit sink, web door and control page. |
| `cmd/` | The programs: `agentsoned` (daemon), `agentsone` (`edge` and `control`), `agentctl`. |
| `runners/` | The runner library, the conformance kit and the runners. |
| `ui/` | The interface: shell, theme system, home and panels. |
| `plugins/` | The plugins that ship with Agents One. |
| `image/`, `compose.yaml` | The container images and the stack. |
| `e2e/` | The end-to-end checks that run against the compose stack. |
| `docs/` | Architecture, deploy notes and these guides. |

Read [Architecture](../architecture.md) first. It explains which part holds which wall, and why a
change to `edge` or `control` deserves more care than a change inside `machine`: root agents live
in `machine`, so only `edge` and `control` can hold a hard wall.

### How a daemon context is built

Every context under `internal/` has the same layout, so you can read one and know them all.
`internal/audit` is the reference.

- `domain/` holds the rules. It does no input or output, and it never imports generated contract
  types.
- `app/` holds the use cases. It depends on `domain` and on the ports in `internal/core`.
- `adapters/` holds the outside: HTTP, SQLite, in-memory fakes.
- `module.go` only wires the context together.

A context reaches another one only through a port in `internal/core`. Never import another
context's package. [CONTRIBUTING.md](../../CONTRIBUTING.md#code-design) has the full list.

## Make a change

1. **Branch from `main`.** Name the branch for what it does.
2. **Write the failing test first.** Run it and read the failure. It must fail because the
   behaviour is missing, not because of a typo. Then write the least code that passes, then tidy
   with the tests green. TESTING.md calls this red, green, refactor, and it is not optional.
3. **For a bug, start with a test that shows the bug.** Put it in the same pull request, and quote
   the failure it showed before the fix.
4. **Keep it small.** One behaviour per pull request is easier to review and quicker to merge.
5. **Run the narrow checks that match your change.** See [Run the checks](#run-the-checks).
6. **Commit with a conventional title**, such as `feat: ...`, `fix: ...` or `docs: ...`.
7. **Open a pull request** and fill in the template: what, why, how it was tested.
8. **Wait for CI.** The `gate` check is the required one. Fix a flaky failure; do not rerun it
   until it goes green.

Never bypass the hooks with `--no-verify`. If a hook is slow or wrong, fix the hook.

### Rules that catch most reviews

- **No warnings or errors** from lint, types, tests, build, logs or CI.
- **Every source file starts with an SPDX header.** `scripts/check-spdx.sh` checks it. JSON files
  are exempt.
- **Wrap every error with context**, such as `fmt.Errorf("hire %s: %w", name, err)`.
- **Comments say why**, not what the code already says.
- **No package-level variables**, except fixed lookup tables with a comment that says why.
- **Use hand-written fakes.** Mocking libraries are not allowed.
- **Use the latest release** of every dependency, tool and action, and pin actions by full commit
  hash.

## Run the checks

| Goal | Command |
| --- | --- |
| Narrow local checks | `make check` |
| Format Go and web files | `make fmt` |
| Lint | `make lint` |
| All tests, as CI runs them | `make test` |
| One Go package, with the race detector | `scripts/heavy go test -race ./internal/audit/...` |
| Web tests | `pnpm test` |
| Coverage gate for Go | `make coverage` |
| Is generated code up to date | `make check-gen` |

`scripts/heavy` limits how many heavy jobs run on your machine at once (set
`AGENTSONE_HEAVY_SLOTS`, default 4). Use it for anything that compiles a lot or runs many tests.

Coverage is a floor, not a goal: domain and app packages 90%, adapters 70%, interface components
80%. A rule with no test blocks a merge even when coverage is high. TESTING.md has the details.

### What CI runs

CI runs only the jobs that match the files you changed, so a docs change does not wait for Go
tests. The jobs are `go`, `go-integration`, `contract`, `ts`, `ui`, `stack`, `shell`, `workflows`,
`image`, `docs`, `spelling`, `spdx`, `secrets`, `dependency-review` and `pr-title`. The `gate` job
passes only when every job that ran has passed. The merge queue runs the same checks again on the
merged result before a change lands on `main`.

### Check your docs

If you change a Markdown file, three checks run on it:

- **markdownlint** checks structure. Config: `.markdownlint-cli2.jsonc`. Run it locally with
  `npx markdownlint-cli2 <file>`.
- **typos** checks spelling. Add a project word to `_typos.toml`, not to the text.
- **lychee** checks that links work.

Write in plain words: short sentences, active voice, and the same term for the same thing. The
[brand voice](../../brand/BRAND.md#voice) says how Agents One speaks.

## The contract is frozen

The API, the schemas, the runner protocol and the generated code are frozen, because many builders
work on top of them. [contract/FREEZE.md](../../contract/FREEZE.md) lists what is frozen and what
is not.

- Only additive changes are allowed. A rename is a major version.
- Do not edit `contract/` or its generated outputs by hand. Ask the contract owner for the
  change.
- A contract change runs `make gen`, then commits the generated files, then `make check-gen`, which
  must find nothing to change.
- Adding a fixture variant, named `<operationId>.<variant>.json`, is not frozen.

[contract/README.md](../../contract/README.md) explains the conventions: `snake_case` fields,
RFC 9457 problem details and cursor lists.

## Test the whole stack

The end-to-end checks start the real compose stack and prove the walls from outside.

```sh
AGENTSONE_IMAGE_TAG=main e2e/run.sh up
e2e/run.sh list
e2e/run.sh all
e2e/run.sh down
```

Each check prints `PASS`, `FAIL` or `SKIP` with a reason. A stage whose feature is not on `main`
yet is `SKIP`, never `PASS`. The script exits 1 when any check fails. `down` deletes the stack's
volumes.

CI builds the images once on `main` and tests them as `sha-<commit>`. A release gives that tested
build its version tags and does not rebuild it, as [Releasing](../../CONTRIBUTING.md#releasing)
describes.

## Work on the interface

The interface is a React app in `ui/`. Every user-visible string is in
`ui/src/i18n/en.json`. Test by what a person sees, with Testing Library, and run the accessibility
check in each test. Look at the page itself, at phone and desktop width, in light and dark, and in
the empty, loaded, error and long-text states. Say "hard wall" and "soft limit" exactly as the
interface does.

## Work on the deploy docs

[Run with Docker Compose](../deploy/compose.md) and [Deploy on Coolify](../deploy/coolify.md) stay
generic: no company VPN, no company sign-in. Test a change to `compose.yaml` against
`e2e/run.sh`, and keep `compose.coolify.yaml` matching `compose.yaml` plus the overlay. CI checks
that they agree.

## Releasing

Maintainers release. The steps are in [CONTRIBUTING.md](../../CONTRIBUTING.md#releasing).

## Getting help and staying safe

- Report a security problem privately, as [SECURITY.md](../../SECURITY.md) describes. Do not open
  a public issue for it.
- By taking part you agree to the [Code of Conduct](../../CODE_OF_CONDUCT.md).
- If you are unsure, open a draft pull request early and ask in it.
