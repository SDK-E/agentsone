#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# gen.sh: regenerate everything derived from the contract. `make gen` runs it,
# `make check-gen` runs it and fails on any diff. Tool versions are pinned in
# go.mod (tool directives) and package.json, so output is deterministic.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "gen: bundle OpenAPI"
if ! pnpm exec redocly bundle contract/openapi/openapi.yaml --ext json --output "$tmp/bundle.json" >"$tmp/bundle.log" 2>&1; then
  cat "$tmp/bundle.log" >&2
  exit 1
fi
if grep -qi 'warning' "$tmp/bundle.log"; then
  cat "$tmp/bundle.log" >&2
  echo "gen: the OpenAPI bundle has warnings; fix them in contract/" >&2
  exit 1
fi

echo "gen: Go types, server interfaces and contract/dist/openapi.json"
go run ./internal/contract/cmd/contractgen -root . -bundle "$tmp/bundle.json"

echo "gen: TypeScript package"
pnpm --silent --filter @agentsone/contract run gen

echo "gen: done"
