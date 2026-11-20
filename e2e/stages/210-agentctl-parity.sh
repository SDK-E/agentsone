# SPDX-License-Identifier: MIT
# shellcheck shell=bash

stage agentctl-parity "agentctl in the machine and the HTTP API give the same answers, and a write by agentctl shows in the API"

# agentctl_parity_session: the signed-in human's session value, which the daemon takes as a bearer
# token, so agentctl and curl act as the same caller.
agentctl_parity_session() {
  awk '$6 == "agentsone_session" { print $7 }' "$WORK/cookies" 2>/dev/null | tail -n 1
}

# agentctl_parity_cli <token> <agentctl args...>: agentctl in the machine, over TCP on loopback.
agentctl_parity_cli() {
  local token="$1"
  shift
  in_machine agentctl --url http://127.0.0.1:7700 --token "$token" --json "$@"
}

# agentctl_parity_same <label> <token> <jq filter> <api path> <agentctl args...>: the filtered
# answers of agentctl and of the API are the same, and the answer is not empty.
agentctl_parity_same() {
  local label="$1" token="$2" filter="$3" path="$4" cli api
  shift 4
  cli="$(agentctl_parity_cli "$token" "$@" 2>&1)" || { fail "$label: agentctl failed" "$cli"; return 0; }
  api="$(human_as_agent "$token" GET "$path" 2>&1)" || { fail "$label: the API call failed" "$api"; return 0; }
  cli="$(printf '%s' "$cli" | jq -S "$filter" 2>&1)" || { fail "$label: agentctl did not print JSON" "$cli"; return 0; }
  api="$(printf '%s' "$api" | jq -S "$filter" 2>&1)" || { fail "$label: the API did not answer JSON" "$api"; return 0; }
  if [ "$cli" = "$api" ] && [ "$cli" != null ] && [ -n "$cli" ]; then
    pass "$label: same answer"
  else
    fail "$label: the answers differ" "agentctl: ${cli} API: ${api}"
  fi
}

stage_agentctl_parity() {
  needs agentctl-parity machine edge || return 0
  door || return 0
  human_signin agentctl-parity || return 0
  local token out id title
  token="$(agentctl_parity_session)"
  if [ -z "$token" ]; then
    fail "agentctl-parity: no session value in the cookie jar"
    return 0
  fi
  if ! out="$(wait_for 60 human_agent_present founder)"; then
    fail "agentctl-parity: the founder is not there 60 seconds after setup" "$out"
    return 0
  fi
  expect_out "agentctl is in the machine and prints its version" '.' in_machine agentctl version

  agentctl_parity_same "plugins list" "$token" '.' /plugins plugins list
  agentctl_parity_same "plugin sources" "$token" '.' /plugin-sources plugins list-sources
  agentctl_parity_same "agent templates" "$token" '.' /agent-templates agents list-templates
  agentctl_parity_same "bus channels" "$token" '.' /bus/channels bus list-channels
  agentctl_parity_same "humans" "$token" '.' /humans humans list
  agentctl_parity_same "agent names" "$token" '[.items[].name] | sort' /agents agents list
  agentctl_parity_same "one agent" "$token" '{name, kind, manager}' /agents/founder agents get founder

  # An error is the same error: agentctl exits non-zero and the API answers 404.
  expect fail "agentctl exits non-zero for a task that does not exist" \
    agentctl_parity_cli "$token" tasks get tsk_00000000000000000000000000
  human_json "the API answers 404 for the same task" 404 '.status == 404' \
    human_as_agent "$token" GET /tasks/tsk_00000000000000000000000000

  # One write by agentctl, read back through the API as the same human.
  title="agentctl parity $(openssl rand -hex 4)"
  id="$(agentctl_parity_cli "$token" tasks create --title "$title" |
    jq -er 'select(.status == "open" and .created_by == "human:'"$HUMAN"'") | .id')" || id=""
  if [ -z "$id" ]; then
    fail "agentctl creates a task as the human"
    return 0
  fi
  pass "agentctl creates a task as the human ($id)"
  human_json "the API shows the task agentctl created" 200 \
    ".id == \"${id}\" and .title == \"${title}\" and .status == \"open\" and .created_by == \"human:${HUMAN}\"" \
    from_browser GET "/v1/tasks/${id}"
  agentctl_parity_same "the new task" "$token" '.' "/tasks/${id}" tasks get "$id"
  expect ok "agentctl cancels the task" agentctl_parity_cli "$token" tasks cancel "$id"
  human_json "the API shows the task cancelled" 200 '.status == "cancelled"' from_browser GET "/v1/tasks/${id}"
}
