#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Tests for build-go.sh against a small fixture module in a temporary folder.
# Needs bash and Go:
#
#   docker run --rm -v "$PWD/image/machine:/src:ro" golang:1.27.4-trixie /src/test/build-go.test.sh
#
# Usage: build-go.test.sh
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
script="$here/../build-go.sh"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
export GOTOOLCHAIN=local GOFLAGS=-mod=mod GOCACHE="$work/cache"

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

expect_file() {
  local name="$1" path="$2"
  if [ -x "$path" ]; then pass "$name"; else fail "$name: $path is not an executable file"; fi
}

expect_absent() {
  local name="$1" path="$2"
  if [ ! -e "$path" ]; then pass "$name"; else fail "$name: $path exists"; fi
}

# write <path> <go package name>: a Go file; main packages get a main function.
write() {
  mkdir -p "$(dirname "$1")"
  if [ "$2" = main ]; then
    printf 'package main\n\nfunc main() {}\n' >"$1"
  else
    printf 'package %s\n\n// X is a placeholder.\nconst X = 1\n' "$2" >"$1"
  fi
}

# fixture <dir>: a module shaped like the repository, without cmd/agentctl or emitters/.
fixture() {
  mkdir -p "$1"
  printf 'module example.test/fixture\n\ngo %s\n' "$(go env GOVERSION | sed 's/^go//')" >"$1/go.mod"
  write "$1/cmd/agentsoned/main.go" main
  write "$1/cmd/agentsoned-fake/main.go" main
  write "$1/runners/echo/echo.go" echo
  write "$1/runners/echo/cmd/agentsone-runner-echo/main.go" main
  write "$1/runners/sdk/protocol/protocol.go" protocol
  mkdir -p "$1/runners/claude"
  echo '{}' >"$1/runners/claude/package.json"
}

repo="$work/repo"
fixture "$repo"
run "$repo" "$work/out"
expect_code "builds a repository with daemon and one runner" 0
expect_file "builds the daemon into bin/" "$work/out/bin/agentsoned"
expect_out "notes a command that does not exist yet" "skip: cmd/agentctl"
expect_absent "builds only the machine commands" "$work/out/bin/agentsoned-fake"
expect_file "names a runner binary after its folder" "$work/out/runners/echo"
expect_absent "skips a runner folder with no Go main" "$work/out/runners/sdk"
expect_absent "skips a runner folder with no Go code" "$work/out/runners/claude"
expect_out "notes a runner folder with no Go main" "skip: runners/sdk"

other="$([ "$(go env GOARCH)" = amd64 ] && echo arm64 || echo amd64)"
GOARCH="$other" run "$repo" "$work/out-cross"
expect_code "cross builds for another architecture" 0
if go version -m "$work/out-cross/bin/agentsoned" | grep -q "GOARCH=$other"; then
  pass "cross build targets GOARCH=$other"
else
  fail "cross build targets GOARCH=$other: $(go version -m "$work/out-cross/bin/agentsoned" | grep GOARCH)"
fi

two="$work/two"
fixture "$two"
write "$two/runners/echo/cmd/second/main.go" main
run "$two" "$work/out-two"
expect_code "refuses a runner folder with two Go mains" 1
expect_out "names the folder with two Go mains" "runners/echo has more than one Go main"

run "$repo"
expect_code "refuses wrong arguments" 2
expect_out "prints usage on wrong arguments" "^usage:"

echo
if [ "$failures" -gt 0 ]; then
  echo "$failures of $total checks failed" >&2
  exit 1
fi
echo "all $total checks passed"
