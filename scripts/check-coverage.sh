#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# check-coverage: gate Go statement coverage per package, by layer (see TESTING.md).
#
# Usage: scripts/check-coverage.sh <coverprofile>
#
# Make the profile with: go test -race -coverprofile=coverage/cover.out ./...
# Do not pass -coverpkg: each package must earn its coverage from its own tests.
#
# Floors (statement coverage, from TESTING.md section E):
#   */domain/*    90
#   */app/*       90
#   */adapters/*  70
#   runners/claude 90 (holds the token proxy, the wall that keeps tokens from the engine)
# Excluded from the gate: cmd/*, generated code and packages (gen, generated, *.gen.go,
# *_gen.go, *.pb.go, zz_generated*.go, internal/contract/*), testdata, and test-support
# packages (any directory whose name ends in "test", such as coretest).
# Packages that match no layer are listed as "unchecked" and never fail the gate.
#
# Exit status: 0 all floors met, 1 at least one package below its floor, 2 bad input.
set -euo pipefail

readonly MIN_DOMAIN=90
readonly MIN_APP=90
readonly MIN_ADAPTERS=70
readonly MIN_RUNNER_CLAUDE=90

usage() {
  echo "usage: scripts/check-coverage.sh <coverprofile>" >&2
}

if [ "$#" -ne 1 ]; then
  usage
  exit 2
fi

profile="$1"
if [ ! -f "$profile" ]; then
  echo "check-coverage: no such file: $profile" >&2
  exit 2
fi
if ! head -n 1 "$profile" | grep -Eq '^mode: (set|count|atomic)$'; then
  echo "check-coverage: $profile is not a Go coverage profile (first line must be 'mode: set|count|atomic')" >&2
  exit 2
fi

# Strip the module path from package names when go.mod is next to this script's parent.
module=""
gomod="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/go.mod"
if [ -f "$gomod" ]; then
  module="$(awk '$1 == "module" { print $2; exit }' "$gomod")"
fi

# layer_of <package>: print "<layer> <floor>". Floor is "-" when the package is not gated.
layer_of() {
  local p="$1/"
  case "$p" in
    */cmd/* | */gen/* | */generated/* | */testdata/* | */internal/contract/* | *test/*)
      echo "excluded -"
      ;;
    */domain/*) echo "domain $MIN_DOMAIN" ;;
    */app/*) echo "app $MIN_APP" ;;
    */adapters/*) echo "adapters $MIN_ADAPTERS" ;;
    */runners/claude/) echo "runner $MIN_RUNNER_CLAUDE" ;;
    *) echo "unchecked -" ;;
  esac
}

# Sum statements per package. A block listed twice counts once, and is covered if any copy ran.
# Generated files are skipped. Output: "<package> <statements> <covered>".
totals="$(
  awk '
    NR == 1 { next }
    NF == 3 {
      file = $1
      sub(/:[^:]*$/, "", file)
      base = file
      sub(/.*\//, "", base)
      if (base ~ /(\.gen\.go|_gen\.go|\.pb\.go)$/ || base ~ /^zz_generated.*\.go$/) next
      pkg = file
      sub(/\/[^\/]*$/, "", pkg)
      if (!($1 in stmts)) {
        stmts[$1] = $2
        owner[$1] = pkg
        hit[$1] = 0
      }
      if ($3 > 0) hit[$1] = 1
    }
    END {
      for (k in stmts) {
        total[owner[k]] += stmts[k]
        if (hit[k]) covered[owner[k]] += stmts[k]
      }
      for (p in total) print p, total[p], covered[p] + 0
    }
  ' "$profile" | LC_ALL=C sort
)"

printf '%-48s %-10s %10s %9s %9s %4s  %s\n' PACKAGE LAYER STATEMENTS COVERED COVERAGE MIN RESULT

gated=0
breaches=0
while read -r pkg total covered; do
  [ -n "$pkg" ] || continue
  name="${pkg#"$module"/}"
  read -r layer floor <<<"$(layer_of "$pkg")"

  if [ "$total" -eq 0 ]; then
    pct="n/a"
  else
    tenths=$((covered * 1000 / total))
    pct="$((tenths / 10)).$((tenths % 10))%"
  fi

  result="skip"
  if [ "$floor" != "-" ]; then
    gated=$((gated + 1))
    # Exact integer comparison: covered/total >= floor/100.
    if [ $((covered * 100)) -ge $((floor * total)) ]; then
      result="ok"
    else
      result="FAIL"
      breaches=$((breaches + 1))
    fi
  fi
  printf '%-48s %-10s %10s %9s %9s %4s  %s\n' "$name" "$layer" "$total" "$covered" "$pct" "$floor" "$result"
done <<<"$totals"

echo
if [ "$gated" -eq 0 ]; then
  echo "PASS: no domain, app or adapters packages in the profile, nothing to gate"
  exit 0
fi
if [ "$breaches" -gt 0 ]; then
  echo "FAIL: $breaches of $gated gated packages are below their coverage floor" >&2
  exit 1
fi
echo "PASS: $gated gated packages meet their coverage floor"
