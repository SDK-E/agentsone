#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Tests for the machine entrypoint. They change real paths (/var/lib/agentsone,
# /home/agents), so they run as root inside a throwaway container, never on a
# workstation:
#
#   docker run --rm -v "$PWD/image/machine:/src:ro" debian:trixie-slim \
#     /src/test/entrypoint.test.sh /src/entrypoint.sh
#
# CI runs them inside the built image against the installed entrypoint.
#
# Usage: entrypoint.test.sh [entrypoint path]   (default /usr/lib/agentsone/entrypoint)
set -euo pipefail

entrypoint="${1:-/usr/lib/agentsone/entrypoint}"
state=/var/lib/agentsone
key=$state/secrets/machine.key

in_container() { [ -f /.dockerenv ] || [ -f /run/.containerenv ]; }
if [ "$(id -u)" -ne 0 ] || ! in_container; then
  echo "run these tests as root inside a container (they change /var/lib and /home)" >&2
  exit 2
fi

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

# reset: remove everything the entrypoint creates, as on a fresh volume.
reset() { rm -rf "$state" /home/agents; }

# run <args...>: run the entrypoint, capture stdout+stderr in $out and the exit code in $code.
run() {
  code=0
  out="$("$entrypoint" "$@" 2>&1)" || code=$?
}

expect_code() {
  local name="$1" want="$2"
  if [ "$code" -eq "$want" ]; then pass "$name"; else
    fail "$name: want exit $want, got $code"
    echo "$out" >&2
  fi
}

# expect_stat <name> <path> <want "mode uid:gid">
expect_stat() {
  local name="$1" path="$2" want="$3" got
  got="$(stat -c '%a %u:%g' "$path" 2>/dev/null || echo missing)"
  if [ "$got" = "$want" ]; then pass "$name"; else fail "$name: $path is '$got', want '$want'"; fi
}

expect_out() {
  local name="$1" pattern="$2"
  if echo "$out" | grep -Eq "$pattern"; then pass "$name"; else
    fail "$name: output has no match for $pattern"
    echo "$out" >&2
  fi
}

# --- first start -------------------------------------------------------------
reset
run true
expect_code "first start exits with the command's status" 0
expect_stat "first start creates machine.key readable by root only" "$key" "600 0:0"
if [ "$(stat -c %s "$key" 2>/dev/null || echo 0)" -eq 32 ]; then
  pass "machine.key holds 32 bytes"
else
  fail "machine.key holds 32 bytes: size is $(stat -c %s "$key" 2>/dev/null || echo missing)"
fi
expect_stat "state folder is root only" "$state" "700 0:0"
expect_stat "secrets folder is root only" "$state/secrets" "700 0:0"
expect_stat "plugins folder is root only" "$state/plugins" "700 0:0"
expect_stat "audit folder is root only" "$state/audit" "700 0:0"
expect_stat "agents home folder is open for traversal" /home/agents "755 0:0"

# --- later starts ------------------------------------------------------------
reset
run true
before="$(sha256sum "$key")"
run true
if [ "$(sha256sum "$key")" = "$before" ]; then pass "later start keeps the existing machine.key"; else
  fail "later start keeps the existing machine.key: key changed"
fi

reset
mkdir -p "$state/secrets"
: >"$key"
run true
if [ "$(stat -c %s "$key")" -eq 32 ]; then pass "empty machine.key is replaced"; else
  fail "empty machine.key is replaced: size is $(stat -c %s "$key")"
fi

reset
mkdir -p "$state/secrets"
head -c 32 /dev/urandom >"$key"
chmod 0644 "$key"
chown 1000:1000 "$key"
run true
expect_stat "loose machine.key is locked to root" "$key" "600 0:0"

reset
mkdir -p "$state/plugins" /home/agents
chmod 0777 "$state" "$state/plugins" /home/agents
chown 1000:1000 "$state" "$state/plugins" /home/agents
run true
expect_stat "loose state folder is locked to root" "$state" "700 0:0"
expect_stat "loose plugins folder is locked to root" "$state/plugins" "700 0:0"
expect_stat "loose agents home folder is reset" /home/agents "755 0:0"

reset
mkdir -p /home/agents/ada
chown 1000:1000 /home/agents/ada
chmod 0700 /home/agents/ada
run true
expect_stat "agent homes inside the volume are left alone" /home/agents/ada "700 1000:1000"

# --- handing over to the command -----------------------------------------------
reset
run sh -c 'echo "args: $1 $2"; exit 7' sh one two
expect_out "runs the command with its arguments" "^args: one two$"
expect_code "passes the command's exit status through" 7

reset
pid_file="$(mktemp)"
# shellcheck disable=SC2016  # $$ and $1 must expand in the command's shell, not here
"$entrypoint" sh -c 'echo "$$" >"$1"' sh "$pid_file" &
started=$!
wait "$started" || true
# exec keeps the entrypoint's pid, so the command reports the pid we started.
if [ "$(cat "$pid_file")" = "$started" ]; then pass "replaces itself with the command (exec)"; else
  fail "replaces itself with the command (exec): command pid $(cat "$pid_file"), entrypoint pid $started"
fi
rm -f "$pid_file"

reset
run
expect_code "refuses to start without a command" 2
expect_out "prints usage without a command" "^usage:"

reset
code=0
out="$(setpriv --reuid=65534 --regid=65534 --clear-groups "$entrypoint" true 2>&1)" || code=$?
expect_code "refuses to start as a non-root user" 1
expect_out "says it needs root" "must run as root"

reset
echo
if [ "$failures" -gt 0 ]; then
  echo "$failures of $total checks failed" >&2
  exit 1
fi
echo "all $total checks passed"
