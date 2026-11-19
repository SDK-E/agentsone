# Contract freeze

From contract version 1.0.0 (`contract/VERSION`) the contract and the seams below are frozen.
About twenty builders work in parallel on top of them, so a change here can break all of them at
once. The owner of the freeze is the orchestrator.

## What is frozen

| Part | Paths |
| --- | --- |
| API description | `contract/openapi/**`, bundled into `contract/dist/openapi.json` |
| Schemas and runner protocol | `contract/schemas/**` |
| Golden fixtures (existing ones) | `contract/fixtures/**` |
| Generated code | `internal/contract/**`, `runners/sdk/protocol`, `packages/contract-ts/src/generated` |
| Daemon seams | `internal/core` (module, deps, ports, caller, config, paths, problems), `internal/api/compose.go` |
| Storage and audit chain | `internal/store`, `internal/auditchain` |
| Edge seam | `internal/edge/edgeenv` |
| Wiring | `cmd/agentsoned/modules.go`, `cmd/agentsone/main.go`, `scripts/gen.sh` |

Not frozen: adding new fixture variants (`<operationId>.<variant>.json`), and everything inside
the package a work package owns, as long as it keeps the seams.

## Package map

Agents One ships as a Docker compose stack with three services: `machine` (the daemon,
`cmd/agentsoned`), `edge` and `control` (both the `agentsone` binary, `cmd/agentsone`, started as
`agentsone edge` and `agentsone control`). There is no VM and no Mac host tool.

| Service | Packages |
| --- | --- |
| machine | `internal/core`, `internal/api`, `internal/store`, `internal/auditchain` and the daemon contexts under `internal/` |
| edge | `internal/edge` (serve), `internal/edge/net`, `internal/edge/gateway`, `internal/edge/budget`, `internal/edge/audit`, `internal/edge/door` |
| control | `internal/edge/control` (the control page) |

The edge works in container folders: state in `/var/lib/agentsone-edge`, configuration in
`/etc/agentsone-edge` (`internal/edge/edgeenv`). Each edge package exports
`Commands(env *edgeenv.Env) []*cobra.Command`.

## How the seams fit

- Every daemon package is a bounded context with `New(core.Deps) *Module` and implements
  `core.Module`. Its `API()` returns the OpenAPI tags it owns, built with the generated
  `<tag>api.Group(impl)`. The API layer (`api.Compose`) refuses a tag served twice; any contract
  operation nobody serves answers 501 problem+json.
- Each OpenAPI tag has its own generated package `internal/contract/api/<tag>api` with a strict
  server interface and an `Unimplemented` type to embed.
- Modules reach each other only through `core.Ports`, which each owner fills in `Provide`.
- Modules keep their own configuration table and read it with `deps.Config.Section("<name>", &v)`.

## Generation rules

`make gen` runs `scripts/gen.sh`: redocly bundle (any bundler warning fails), then
`internal/contract/cmd/contractgen`, then the TypeScript generator. `make check-gen` fails on any
difference, and CI runs it.

- The bundle keeps its schemas exact, but `$id` and `$schema` are removed from embedded schemas
  (they would re-base references), and whole-file schemas named after their file
  (`event.schema`) take the name of their alias component (`Event`).
- oapi-codegen cannot express a schema typed with several non-null types, such as the JSON-RPC
  id (`["string", "integer"]`). For code generation only, such a schema is untyped (Go `any`).
  `contract/dist/openapi.json` and validation keep the exact types.
- Every operation may answer 501 `urn:agentsone:problem:not-implemented`. The bundle step adds the
  shared `NotImplemented` response to every operation that does not declare a 501 itself; the
  sources under `contract/openapi` stay unchanged.
- Behaviour that the schema cannot show is written in the operation description. Since 1.0.2:
  `updateHuman` requires `current_password` for a human's own password change (400 `validation`
  when missing or wrong), ends the human's other sessions and rotates the caller's cookie (a reset
  of another human ends all that human's sessions); `deleteMemory` is open to humans,
  who cannot edit memory.
- Runner protocol version: 1.1 since contract 1.0.3. Minor versions only add optional fields or
  notifications (see "Version negotiation" in `schemas/runner/PROTOCOL.md`). 1.1 adds
  `credentials.update`; `transcript.ndjson` stays the 1.0 transcript and `transcript-1.1.ndjson`
  covers 1.1.
- A parameter, response, request body or header component that shares its name with a schema is
  renamed for code generation only (`Cursor` becomes `CursorParameter`).

## Names that keep their old spelling

Descriptions use the compose terms (edge, machine container, cgroup `/agents/<id>`). These
identifiers date from the first design. Renaming any of them would break generated clients, so they
stay until a major version, and their descriptions say what they mean today:

| Name | Where | Means now |
| --- | --- | --- |
| tag `host`, paths `/host/*`, operation ids `getDoorHealth`, `listFreezes`, ... | OpenAPI | The door API called only by the edge service. |
| security scheme `hostDoor` | OpenAPI | Transport identity of the edge service. |
| problem type `urn:agentsone:problem:host-only` | Problem | Only the edge service may call this. |
| actor kind `host:<part>`, `CallerHost` | Actor, event `actor`, `internal/core` | The edge service. |
| audit record origin `host` | `audit-record.schema.json` | Written by the edge. |
| `Agent.unit` pattern `agent@<name>.service`, event examples with it | `Agent` schema, event catalog | The unit name of an agent; the pattern is unchanged. |
| `host_acked_seq`, `HostBudgetPush` and similar field and schema names | OpenAPI | Set by the edge. |

## How to request a change

1. Ask the orchestrator first, with the reason and who it affects.
2. If agreed, one pull request changes the contract, bumps `contract/VERSION` (semver: patch for
   wording, minor for an added operation or optional field, major for anything that breaks a
   caller), regenerates with `make gen`, and updates the fixtures and the affected packages.
3. The orchestrator tells every builder whose seam moved.
