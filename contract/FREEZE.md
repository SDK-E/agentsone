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
| Host seam | `internal/host/hostenv` |
| Wiring | `cmd/agentsoned/modules.go`, `cmd/agentsone/main.go`, `scripts/gen.sh` |

Not frozen: adding new fixture variants (`<operationId>.<variant>.json`), and everything inside
the package a work package owns, as long as it keeps the seams.

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
- A parameter, response, request body or header component that shares its name with a schema is
  renamed for code generation only (`Cursor` becomes `CursorParameter`).

## How to request a change

1. Ask the orchestrator first, with the reason and who it affects.
2. If agreed, one pull request changes the contract, bumps `contract/VERSION` (semver: patch for
   wording, minor for an added operation or optional field, major for anything that breaks a
   caller), regenerates with `make gen`, and updates the fixtures and the affected packages.
3. The orchestrator tells every builder whose seam moved.
