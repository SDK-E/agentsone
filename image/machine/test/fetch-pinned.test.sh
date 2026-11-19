#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Tests for fetch-pinned.sh. Downloads come from file:// URLs in a temporary
# folder, so no network is used. Needs bash, curl and GNU sha256sum, so it
# runs in the built image (CI does this):
#
#   docker run --rm -v "$PWD/image/machine:/src:ro" --entrypoint /src/test/fetch-pinned.test.sh \
#     agentsone-machine:dev
#
# Usage: fetch-pinned.test.sh
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
script="$here/../fetch-pinned.sh"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

failures=0
total=0
out=""
code=0

pass() {
  total=$((total + 1))
  echo "ok   - $1"
}
fail() {
  total=$((total + 1))
  failures=$((failures + 1))
  echo "FAIL - $1" >&2
}

run() {
  code=0
  out="$("$script" "$@" 2>&1)" || code=$?
}

expect_code() {
  local name="$1" want="$2"
  if [ "$code" -eq "$want" ]; then pass "$name"; else
    fail "$name: want exit $want, got $code"
    echo "$out" >&2
  fi
}

expect_out() {
  local name="$1" pattern="$2"
  if echo "$out" | grep -Eq "$pattern"; then pass "$name"; else
    fail "$name: output has no match for $pattern"
    echo "$out" >&2
  fi
}

expect_absent() {
  local name="$1" path="$2"
  if [ ! -e "$path" ]; then pass "$name"; else fail "$name: $path exists"; fi
}

echo "tool for arm64" >"$work/tool-arm64.tar"
echo "tool for amd64" >"$work/tool-amd64.tar"
sum() { sha256sum "$1" | cut -d ' ' -f 1; }
wrong=0000000000000000000000000000000000000000000000000000000000000000

cat >"$work/pins" <<EOF
# name  arch  sha256  url

tool  arm64  $(sum "$work/tool-arm64.tar")  file://$work/tool-arm64.tar
tool  amd64  $(sum "$work/tool-amd64.tar")  file://$work/tool-amd64.tar
bad   arm64  $wrong  file://$work/tool-arm64.tar
twice arm64  $(sum "$work/tool-arm64.tar")  file://$work/tool-arm64.tar
twice arm64  $(sum "$work/tool-arm64.tar")  file://$work/tool-arm64.tar
gone  arm64  $(sum "$work/tool-arm64.tar")  file://$work/missing.tar
EOF

run "$work/pins" tool amd64 "$work/out-amd64"
expect_code "fetches the pinned file for the arch" 0
if cmp -s "$work/out-amd64" "$work/tool-amd64.tar"; then pass "writes the file for the requested arch"; else
  fail "writes the file for the requested arch: content differs"
fi

run "$work/pins" bad arm64 "$work/out-bad"
expect_code "rejects a file whose checksum differs" 1
expect_out "names the checksum failure" "checksum"
expect_absent "leaves no file behind after a checksum failure" "$work/out-bad"
expect_absent "leaves no partial file after a checksum failure" "$work/out-bad.part"

run "$work/pins" tool riscv64 "$work/out-none"
expect_code "rejects a name and arch without a pin" 1
expect_out "names the missing pin" "no pin for tool riscv64"
expect_absent "downloads nothing without a pin" "$work/out-none"

run "$work/pins" twice arm64 "$work/out-twice"
expect_code "rejects a name and arch pinned twice" 1
expect_out "names the duplicate pin" "more than one pin for twice arm64"

run "$work/pins" gone arm64 "$work/out-gone"
expect_code "fails when the download fails" 1
expect_absent "leaves no file behind after a failed download" "$work/out-gone"

run "$work/pins" tool
expect_code "refuses wrong arguments" 2
expect_out "prints usage on wrong arguments" "^usage:"

echo
if [ "$failures" -gt 0 ]; then
  echo "$failures of $total checks failed" >&2
  exit 1
fi
echo "all $total checks passed"
