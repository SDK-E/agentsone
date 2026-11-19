#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Self-test for scripts/check-version.sh: a good tree passes, and each kind of drift fails.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
check="$here/check-version.sh"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

pass=0
failures=0

# tree <name> <version-file-content> <default-tag-in-compose> <default-tag-in-coolify>
tree() {
  local dir="$work/$1"
  mkdir -p "$dir"
  printf '%s\n' "$2" > "$dir/VERSION"
  for file in compose.yaml compose.coolify.yaml; do
    local tag="$3"
    [ "$file" = compose.coolify.yaml ] && tag="$4"
    cat > "$dir/$file" <<EOF
services:
  machine:
    image: ghcr.io/sdk-e/agentsone-machine:\${AGENTSONE_IMAGE_TAG:-$tag}
  edge:
    image: ghcr.io/sdk-e/agentsone-edge:\${AGENTSONE_IMAGE_TAG:-$tag}
EOF
  done
}

# expect <ok|fail> <name> [release tag]
expect() {
  local want="$1" name="$2" status=0
  shift 2
  AGENTSONE_ROOT="$work/$name" "$check" "$@" > /dev/null 2>&1 || status=$?
  if { [ "$want" = ok ] && [ "$status" -eq 0 ]; } || { [ "$want" = fail ] && [ "$status" -eq 1 ]; }; then
    pass=$((pass + 1))
  else
    echo "FAIL: $name $* expected $want, exit status $status" >&2
    failures=$((failures + 1))
  fi
}

tree good 0.1.0 v0.1.0 v0.1.0
expect ok good
expect ok good v0.1.0
expect fail good v0.1.1
expect fail good 0.1.0

tree compose-behind 0.2.0 v0.1.0 v0.2.0
expect fail compose-behind

tree coolify-behind 0.2.0 v0.2.0 v0.1.0
expect fail coolify-behind

tree floating-tag 0.1.0 edge edge
expect fail floating-tag

tree short-version 0.1 v0.1 v0.1
expect fail short-version

tree v-prefix 0.1.0 v0.1.0 v0.1.0
printf 'v0.1.0\n' > "$work/v-prefix/VERSION"
expect fail v-prefix

tree suffix 0.1.0-rc1 v0.1.0-rc1 v0.1.0-rc1
expect fail suffix

tree pinned 0.1.0 v0.1.0 v0.1.0
printf '    image: ghcr.io/sdk-e/agentsone-control:v0.1.0\n' >> "$work/pinned/compose.yaml"
expect fail pinned

tree no-version 0.1.0 v0.1.0 v0.1.0
rm "$work/no-version/VERSION"
expect fail no-version

if [ "$failures" -ne 0 ]; then
  echo "check-version self-test: $failures failed, $pass passed" >&2
  exit 1
fi
echo "check-version self-test: $pass checks passed"
