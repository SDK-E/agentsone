#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Fail when a tracked source file lacks an SPDX-License-Identifier line in its first 5 lines.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

missing=0
checked=0

while IFS= read -r -d '' file; do
  case "$file" in
    contract/fixtures/* | pnpm-lock.yaml | */pnpm-lock.yaml | package-lock.json | go.sum) continue ;;
  esac
  [ -f "$file" ] || continue
  checked=$((checked + 1))
  if ! head -n 5 "$file" | grep -q 'SPDX-License-Identifier:'; then
    echo "missing SPDX line: $file" >&2
    missing=$((missing + 1))
  fi
done < <(git ls-files -z -- '*.go' '*.ts' '*.tsx' '*.js' '*.mjs' '*.cjs' '*.sh' '*.css' '*.yml' '*.yaml' '*.toml' 'scripts/heavy')

if [ "$missing" -gt 0 ]; then
  echo "$missing of $checked source files lack an SPDX-License-Identifier line in the first 5 lines" >&2
  exit 1
fi
echo "SPDX check passed ($checked files)"
