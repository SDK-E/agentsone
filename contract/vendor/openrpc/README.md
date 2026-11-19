<!-- SPDX-License-Identifier: MIT -->

# Vendored OpenRPC meta-schema

`meta-schema-1.3.json` is the JSON Schema that describes an OpenRPC 1.3 document. CI uses it to
validate `contract/schemas/runner/runner.openrpc.json` (`TestRunnerOpenRPCDocument` in
`internal/contract`, run by the `contract` job of `pr.yml`).

- Source: the `@open-rpc/meta-schema` package, version 1.14.9 (latest release, 8 May 2024), the
  `openrpcDocument` export of `index.js`, written out as JSON. The repository's own `schema.json`
  holds a placeholder for the `openrpc` version list, so the released package is the source.
  <https://github.com/open-rpc/meta-schema>
- Licence: Apache License 2.0, copyright the OpenRPC authors. The licence text is in `LICENSE.md`
  next to the schema. The schema is unmodified apart from JSON formatting.
- `json-schema-meta-schema.json` is the JSON Schema meta-schema the OpenRPC one refers to
  (`https://meta.json-schema.tools`). Source: the `@json-schema-tools/meta-schema` package, version
  1.8.3 (`schema.json`, latest release, 17 October 2026), Apache License 2.0, same licence text.
  <https://github.com/json-schema-tools/meta-schema>
- It accepts `openrpc` values 1.3.2, 1.3.1, 1.3.0 and older; the runner document uses 1.3.2.
- Both files declare a `$schema` URL that is no published draft, so the test drops that keyword in
  memory and compiles them as draft 07.

To update, take the schema from a newer release of the package, replace the file and run
`go test ./internal/contract/...`.
