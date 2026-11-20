#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Tests for the machine health check, against the real daemon. They start
# agentsoned, so they run inside the built image (CI does this):
#
#   docker run --rm -v "$PWD/image/machine:/src:ro" --entrypoint /src/test/healthcheck.test.sh \
#     agentsone-machine:dev
#
# Usage: healthcheck.test.sh [healthcheck path]   (default /usr/lib/agentsone/healthcheck)
set -euo pipefail

healthcheck="${1:-/usr/lib/agentsone/healthcheck}"
daemon=/usr/lib/agentsone/bin/agentsoned
socket=/run/agentsone/api.sock

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
  out="$("$healthcheck" 2>&1)" || code=$?
}

expect_code() {
  local name="$1" want="$2"
  if [ "$code" -eq "$want" ]; then pass "$name"; else
    fail "$name: want exit $want, got $code"
    echo "$out" >&2
  fi
}

run
expect_code "reports unhealthy while the daemon is not running" 1
if echo "$out" | grep -q "127.0.0.1:7700"; then pass "says which address did not answer"; else
  fail "says which address did not answer: output is '$out'"
fi

state="$(mktemp -d)"
config="$state/config.toml"
printf 'state_dir = "%s"\n' "$state" >"$config"
"$daemon" -config "$config" >"$state/daemon.log" 2>&1 &
pid=$!
for _ in $(seq 1 100); do
  [ -S "$socket" ] && break
  kill -0 "$pid" 2>/dev/null || break
  sleep 0.1
done

run
expect_code "reports healthy once the daemon serves the setup endpoint" 0

kill -TERM "$pid"
wait "$pid" || true
rm -rf "$state"

echo
if [ "$failures" -gt 0 ]; then
  echo "$failures of $total checks failed" >&2
  exit 1
fi
echo "all $total checks passed"
