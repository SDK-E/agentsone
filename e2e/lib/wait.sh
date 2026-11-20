# SPDX-License-Identifier: MIT
# shellcheck shell=bash
#
# Waiting for state the stack reaches on its own. Sourced by e2e/run.sh before the stage files.

# wait_for <seconds> <command...>: run the command once a second until it succeeds; fail with its
# last output when the deadline passes. For state the stack reaches on its own, never to hide a
# flaky answer.
wait_for() {
  local deadline=$((SECONDS + $1)) out
  shift
  until out="$("$@" 2>&1)"; do
    if [ "$SECONDS" -ge "$deadline" ]; then
      printf '%s\n' "$out"
      return 1
    fi
    sleep 1
  done
}

# seed_installed: GET /v1/setup through the web door says the Small Seed is installed.
seed_installed() { curl -sS -m 10 "${DOOR}/v1/setup" | jq -ce '.seed_installed == true'; }
