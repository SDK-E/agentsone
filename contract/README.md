<!-- SPDX-License-Identifier: MIT -->

# Contract

The frozen contract every part of Agents One builds against. Code generators read these files;
CI fails when generated code drifts from them.

## Files

| Path | What it holds |
| --- | --- |
| `VERSION` | Contract version (semantic versioning). |
| `openapi/openapi.yaml` | Root of the machine API description (OpenAPI 3.1): servers, security, tags, path index. |
| `openapi/paths/<group>.yaml` | Operations, one file per tag (bounded context). |
| `openapi/components/schemas/<group>.yaml` | Named schemas for each group; `common.yaml` holds ids, names, times, problems. |
| `openapi/components/parameters.yaml`, `responses.yaml` | Shared parameters and problem responses. |
| `schemas/` | JSON Schemas for the runner protocol, event envelope, plugin manifest, theme, memory record, audit record and agent template. The API references them whole. |
| `fixtures/api/<operationId>[.<variant>].json` | Golden responses served by the fake daemon: `{"status": <http>, "body": <body>}`. Variants include `empty`, `long-text` and failing cases such as `error`, `validation`, `conflict`, `root-only`. |
| `redocly.yaml` | Lint and bundle configuration. |
| `vendor/openrpc/` | The OpenRPC 1.3 meta-schema (Apache-2.0) that CI validates the runner protocol against. |

## Lint and bundle

Run from this folder:

```sh
pnpm dlx --package=@redocly/cli redocly lint --config redocly.yaml
pnpm dlx --package=@redocly/cli redocly bundle openapi/openapi.yaml --config redocly.yaml -o openapi.bundle.yaml
```

Lint uses the `recommended-strict` ruleset and must report zero warnings. The bundle is a build
output; do not commit it.

## API conventions

- Base path `/v1` on `http://127.0.0.1:7700` and on the Unix socket `/run/agentsone/api.sock`.
  The web door in the `edge` service reaches the daemon on TCP port 7700 as a trusted proxy. The
  `host` group is served only to the edge service.
- Callers: humans with the `agentsone_session` cookie (through the web door), agents by socket
  peer credentials and cgroup `/agents/<id>`, scoped bearer tokens (agent and panel plugin tokens),
  and the edge service.
- API fields are `snake_case`, including the memory record, the audit record and event `data`
  (see [Naming](#naming)).
- Ids are a type prefix plus a ULID. Agent and human names are Linux usernames. Times are RFC 3339 UTC.
- Lists use `cursor` and `limit`, and return `{items, next_cursor}`.
- Errors are RFC 9457 problem details with stable `urn:agentsone:problem:*` types (listed on the
  `Problem` schema).
- Streams are Server-Sent Events whose `data` is one JSON frame; `x-stream` names the frame schema.
  Terminals use a WebSocket (`connectPty`).
- Limits carry `wall`: `hard` is held by `edge`, a separate container that `machine` cannot reach,
  `soft` by the daemon inside `machine`, where a root agent can bypass it.
- Domain actions with rules are their own operations (`hireAgent`, `retireAgent`, `wakeAgent`,
  `assignTask`, `completeTask`, `applyFreeze`), not generic edits.

## Naming

Four rules decide how a property is spelled.

1. **API resources are `snake_case`.** Everything the API returns or accepts as a resource: the
   memory record, the audit record, the `data` of every event type in `schemas/events/catalog.json`,
   and the shared definitions they use. Example: a memory record has `created_at`, an audit record
   has `prev_hash`, and `agentsone.disk.usage.high` carries `used_percent`.
2. **File formats authored in plugin repositories are `camelCase`**, like Claude Code's
   `plugin.json`: the plugin manifest, the theme and the agent template. Example: a manifest lists
   `eventTypes` and a template sets `limits.memoryMiB`. When the API returns one of these files, it
   returns it unchanged.
3. **The runner protocol is `camelCase`**, following JSON-RPC and MCP (`schemas/runner/**`).
   Example: `wake` carries `turnId`.
4. **CloudEvents attributes are lowercase**, as the CloudEvents specification requires. Example:
   `datacontenttype`, `chartscope` and `correlationid`.

If one shared definition would need two spellings, it is split into two definitions.

Schema names are unique across the contract. Two schemas with one name must have one meaning; two
meanings get two names (for example `Emitter` is the API resource and `ManifestEmitter` is the
declaration inside a plugin manifest). `redocly bundle` must report no "same name, different
content" warnings.

## Versioning and stability

- `VERSION` and `info.version` in `openapi/openapi.yaml` always match.
- The contract was frozen at 1.0.0. Any change after the freeze needs a version bump by the contract
  owner: patch for wording only, minor for additions (new operations, optional fields, new enum
  values clients must tolerate), major for anything that breaks a generated client.
- Additions to problem types or enums are minor changes; clients must treat unknown values as
  unknown, not as errors.
- Fixtures change with the contract in the same pull request.
- Every operation may answer 501 `urn:agentsone:problem:not-implemented`. The sources do not repeat
  it: the bundle step of `scripts/gen.sh` adds the shared `NotImplemented` response to every
  operation of `contract/dist/openapi.json`, so generated code and response validation cover it.

## Changelog

### 1.1.1

Descriptions and examples only. No schema change.

- The machine runs no systemd. `listServices` reports the services the daemon reports, today
  `agentsoned.service` and `machine-recipe.service`; `Process.unit` is always null, because the daemon
  supervises agent runners itself. The descriptions of the service operations, `Process.unit`,
  `Process.agent`, `Service.agent` and the `unit` path parameter say so.
- The fixtures `listProcesses`, `listProcesses.long-text` and `listServices` match what the daemon
  really returns: runner and daemon command lines, unit null, two services.

### 1.1.0

Additive (minor: a new operation).

- New operation `getPluginFile`, `GET /v1/plugins/{plugin_id}/files?path=<path>`: one file of a
  panel plugin, as JSON (`PluginFile`: path, media type, `utf-8` or `base64` content, size), for
  the interface's panel host. Humans only. The plugin must be installed and enabled, the
  normalised path must lie inside the folder of a declared panel entry, and the file is at most
  4 MiB. The response carries `Content-Security-Policy: sandbox; default-src 'none'`,
  `X-Content-Type-Options: nosniff`, `Cross-Origin-Resource-Policy: same-origin` and
  `Cache-Control: no-store`. It is JSON, never a document, so visiting the address cannot run
  the file.
- `FileEntry.link_target` (optional, string or null): where a symlink points.
- New fixtures `getPluginFile` and `getPluginFile.error` (the plugin is turned off); the
  `listFiles` symlink now names its target.

### 1.0.5

Additive.

- `Agent.state_reason` and `StateFrame.state_reason` (optional, string or null, up to 500
  characters): plain words that say why an agent is `failed`, for example "The model gateway is not
  reachable." The daemon sets it when a runner cannot start or stops with an error and clears it when
  the agent runs again.
- New fixtures `getAgent.failed` and `getAgent.failed-long-text`.

### 1.0.4

Additive.

- New problem type `urn:agentsone:problem:method-not-allowed` (405). The `Allow` header lists the
  accepted methods. No operation declares it: it is for paths outside `/v1`, such as the web
  interface server.

### 1.0.3

Additive. Runner protocol 1.1 (the API is unchanged).

- New daemon-to-runner notification `credentials.update` with `gatewayToken`,
  `gatewayTokenExpiresAt`, `mcpToken` and `mcpTokenExpiresAt`. The runner uses the latest tokens for
  every new model call and MCP request, and sends no response. The daemon sends it only on a
  connection that negotiated 1.1; a runner that speaks only 1.0 is unaffected.
- `PROTOCOL.md` has a Credentials section and rules K1 to K3, the OpenRPC document lists the method
  (`x-since` 1.1), and the fixtures have a valid and two invalid messages plus
  `transcript-1.1.ndjson`. `transcript.ndjson` stays the 1.0 conformance transcript.

### 1.0.2

All changes are additive and backward compatible.

- `Plugin.health_detail` (optional, string or null): plain words that say why a plugin is
  `degraded` or `failed`.
- `updateHuman` accepts `current_password`. A human who changes their own password must send it
  (a missing or wrong value answers 400 `validation` with an issue at `/current_password`);
  another human resetting the password does not. A human's own change ends their other sessions and
  rotates the caller's session (`Set-Cookie` on the 200); resetting another human's password ends
  all of that human's sessions.
- `updateHuman` declares 429 (`rate-limited`, with `Retry-After`): a wrong `current_password`
  counts against the sign-in throttle.
- `deleteMemory`: humans may delete any record, with an optional `reason` query parameter kept in
  the audit record. Humans cannot edit records.
- New fixture variants: `createTask.error`, `createTask.frozen`, `verifyAuditChain.broken`,
  `verifyAuditChain.empty`, `getBudgetSplit.empty`, `getBudgetSplit.long-text`,
  `listPlugins.long-text`, `listPlugins.error`, `searchPluginCatalog.error`,
  `listHumans.long-text`, `getEngineLicence.not-accepted` and `getEngineFetchStatus.failed`.

### 1.0.1

All changes are additive and backward compatible.

- Event catalog 1.1.0: new types `agentsone.setup.licence.accepted` and `agentsone.setup.first-human.created` (emitter `setup`) and the plugin
  types `agentsone.plugin.installed`, `.removed`, `.enabled`, `.disabled` and `.failed` (emitter
  `plugin`), each with a data schema, an example and event fixtures.
- New problem type `urn:agentsone:problem:not-implemented` (501), answered by any operation the
  daemon does not serve yet. Every operation in the bundle declares the 501 response.
- `createTask` declares a 403 response (`forbidden`).
- `registerEmitter`, `enableEmitter`, `disableEmitter` and `receiveWebhook` declare a 503
  response (`unavailable`).
- Every 429 response carries a `Retry-After` header (the shared `TooManyRequests` response did
  already; the bundle check now guards it).
- Runner protocol stays 1.0. `TimerFired.name` follows the schedule name (1 to 80 characters, was
  a 64-character kebab-case pattern) and `TimerFired.note` follows the schedule message (up to
  2000 characters, was one line of 255). The old values were stricter than the API that feeds them.
- Descriptions now use the compose terms: the web door and caller are the `edge` service, the
  daemon runs in the `machine` container, and an agent's cgroup is `/agents/<id>`. No name,
  value or shape changed (see "Names that keep their old spelling" in `FREEZE.md`).
- CI validates `schemas/runner/runner.openrpc.json` against the vendored OpenRPC 1.3 meta-schema in
  `vendor/openrpc/`.

### 1.0.0

First frozen contract.
