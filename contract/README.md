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
  The `host` group is served only on the vsock door to the host tool.
- Callers: humans with the `agentsone_session` cookie, agents by socket peer credentials and
  cgroup `agent@<name>.service`, scoped bearer tokens (agent and panel plugin tokens), and the
  host tool over vsock.
- API fields are `snake_case`, including the memory record, the audit record and event `data`
  (see [Naming](#naming)).
- Ids are a type prefix plus a ULID. Agent and human names are Linux usernames. Times are RFC 3339 UTC.
- Lists use `cursor` and `limit`, and return `{items, next_cursor}`.
- Errors are RFC 9457 problem details with stable `urn:agentsone:problem:*` types (listed on the
  `Problem` schema).
- Streams are Server-Sent Events whose `data` is one JSON frame; `x-stream` names the frame schema.
  Terminals use a WebSocket (`connectPty`).
- Limits carry `wall`: `hard` is held by the host tool outside the VM, `soft` by the daemon inside
  it, where a root agent can bypass it.
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
- The contract is frozen at 1.0.0. Any change after the freeze needs a version bump by the contract
  owner: patch for wording only, minor for additions (new operations, optional fields, new enum
  values clients must tolerate), major for anything that breaks a generated client.
- Additions to problem types or enums are minor changes; clients must treat unknown values as
  unknown, not as errors.
- Fixtures change with the contract in the same pull request.
