# SPDX-License-Identifier: MIT
# shellcheck shell=bash
#
# Claude engine opt-in: the engine is off by default. Setup ends with the first human, and nothing
# downloads the engine until a human turns it on in Settings and accepts its licence. Runs after
# setup-page, so the first human exists and the daemon has been up for a while.

stage engine-opt-in "setup ends with the first human, and the Claude engine is not downloaded without opt-in"

# The host the pinned engine downloads from (internal/enginefetch/domain/claude-code.pin.json).
ENGINE_OPT_IN_HOST='downloads\.claude\.ai'

# engine_opt_in_absent <service>: the service's log never names the engine download host.
engine_opt_in_absent() {
  local logs
  logs="$(compose logs --no-color "$1" 2>&1)" || {
    printf '%s\n' "$logs"
    return 1
  }
  ! printf '%s' "$logs" | grep -Eq "$ENGINE_OPT_IN_HOST"
}

# engine_opt_in_empty_dir: the engines volume holds no file and no folder.
engine_opt_in_empty_dir() {
  local found
  found="$(in_machine find /var/lib/agentsone-engines -mindepth 1 -print -quit 2>&1)" || {
    printf '%s\n' "$found"
    return 1
  }
  [ -z "$found" ] || {
    printf 'found: %s\n' "$found"
    return 1
  }
}

stage_engine_opt_in() {
  needs engine-opt-in machine edge || return 0
  door || return 0
  expect_http "after the first human, setup is done with no engine step" '200' '"phase":"ready"' \
    curl -sS -m 10 "${DOOR}/v1/setup"
  expect_http "nobody accepted the engine licence" '200' '"licence_accepted":false' \
    curl -sS -m 10 "${DOOR}/v1/setup"
  expect_http "the engine waits for a human to turn it on" '200' '"state":"waiting-licence"' \
    curl -sS -m 10 "${DOOR}/v1/setup"
  expect ok "the engines volume is empty" engine_opt_in_empty_dir
  expect ok "the edge log shows no request to the engine download host" engine_opt_in_absent edge
  expect ok "the machine log shows no engine download" engine_opt_in_absent machine
  expect ok "the edge proof log has no entry for the engine download host" engine_opt_in_no_proof
}

# engine_opt_in_no_proof: the edge proof log, read as the control page reads it, answers and never
# names the engine download host.
engine_opt_in_no_proof() {
  local proof
  proof="$(as_control '/v1/audit/proof?limit=1000' -f)" || {
    printf '%s\n' "$proof"
    return 1
  }
  ! printf '%s' "$proof" | grep -Eq "$ENGINE_OPT_IN_HOST"
}
