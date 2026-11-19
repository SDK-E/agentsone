#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Tests for scripts/check-coverage.sh. Runs it against the sample profiles in
# scripts/testdata/check-coverage/ and checks exit codes and output.
#
# Usage: scripts/test-check-coverage.sh
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
script="$here/check-coverage.sh"
data="$here/testdata/check-coverage"

failures=0
total=0
out=""
code=0

# run <args...>: run the script, capture stdout+stderr in $out and the exit code in $code.
run() {
  code=0
  out="$("$script" "$@" 2>&1)" || code=$?
}

pass() { total=$((total + 1)); echo "ok   - $1"; }
fail() { total=$((total + 1)); failures=$((failures + 1)); echo "FAIL - $1" >&2; }

expect_code() {
  local name="$1" want="$2"
  if [ "$code" -eq "$want" ]; then pass "$name: exit $want"; else
    fail "$name: want exit $want, got $code"
    echo "$out" >&2
  fi
}

# expect_row <name> <package> <regexp that the package's row must match>
expect_row() {
  local name="$1" pkg="$2" pattern="$3" row
  row="$(echo "$out" | grep -E "^${pkg}[[:space:]]" || true)"
  if [ -n "$row" ] && echo "$row" | grep -Eq "$pattern"; then pass "$name"; else
    fail "$name: row for $pkg is '$row', want match for $pattern"
  fi
}

expect_out() {
  local name="$1" pattern="$2"
  if echo "$out" | grep -Eq "$pattern"; then pass "$name"; else
    fail "$name: output has no match for $pattern"
    echo "$out" >&2
  fi
}

# --- input errors exit 2 ---------------------------------------------------
run
expect_code "no arguments" 2
expect_out "no arguments prints usage" "^usage:"

run "$data/does-not-exist.out"
expect_code "missing file" 2

run "$data/not-a-profile.out"
expect_code "file without a mode header" 2

# --- a profile that meets every floor --------------------------------------
run "$data/pass.out"
expect_code "pass.out" 0
expect_row "domain at 100 percent is ok" "internal/agents/domain" "domain[[:space:]]+10[[:space:]]+10[[:space:]]+100\.0%[[:space:]]+90[[:space:]]+ok"
expect_row "generated file is ignored" "internal/agents/domain" "[[:space:]]10[[:space:]]+10[[:space:]]"
expect_row "exactly 90 percent passes" "internal/memory/domain" "90\.0%[[:space:]]+90[[:space:]]+ok"
expect_row "duplicate blocks count once" "internal/agents/app" "app[[:space:]]+20[[:space:]]+18[[:space:]]+90\.0%[[:space:]]+90[[:space:]]+ok"
expect_row "exactly 70 percent passes for adapters" "internal/store/adapters/sqlite" "adapters[[:space:]]+10[[:space:]]+7[[:space:]]+70\.0%[[:space:]]+70[[:space:]]+ok"
expect_row "cmd is excluded" "cmd/agentsoned" "excluded"
expect_row "generated package is excluded" "internal/contract/gen" "excluded"
expect_row "test helper package is excluded" "internal/core/coretest" "excluded"
expect_row "package without a layer is not gated" "internal/bus" "unchecked"
expect_out "pass summary" "^PASS: 4 gated packages"

# --- a profile that breaches floors ----------------------------------------
run "$data/fail.out"
expect_code "fail.out" 1
expect_row "domain at 80 percent fails" "internal/agents/domain" "80\.0%[[:space:]]+90[[:space:]]+FAIL"
expect_row "app at 90 percent passes" "internal/agents/app" "90\.0%[[:space:]]+90[[:space:]]+ok"
expect_row "adapters at 60 percent fails" "internal/store/adapters/sqlite" "60\.0%[[:space:]]+70[[:space:]]+FAIL"
expect_row "69.9 percent is shown rounded down and fails" "internal/bus/adapters/memory" "69\.9%[[:space:]]+70[[:space:]]+FAIL"
expect_out "fail summary" "^FAIL: 3 of 4 gated packages"

# --- nothing to gate -------------------------------------------------------
run "$data/ungated.out"
expect_code "ungated.out" 0
expect_out "ungated note" "^PASS: no domain, app or adapters packages"

echo
if [ "$failures" -gt 0 ]; then
  echo "$failures of $total checks failed" >&2
  exit 1
fi
echo "all $total checks passed"
