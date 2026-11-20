# SPDX-License-Identifier: MIT
# shellcheck shell=bash

stage memory "an agent writes memory and search finds it, for the ones who may read it and no one else"

# The agents are tokens the human issued (no runner needs to answer): the founder writes, the
# router below it reads.
stage_memory() {
  needs memory machine edge || return 0
  door || return 0
  human_signin memory || return 0
  local run common private founder router id_common id_private out
  # One unique word per record, so a search finds this run's record and nothing else.
  run="$(openssl rand -hex 4)"
  common="zqcommon${run}"
  private="zqprivate${run}"
  if ! out="$(wait_for 60 human_agent_present founder)"; then
    fail "memory: the founder is not there 60 seconds after setup" "$out"
    return 0
  fi
  if ! founder="$(human_token founder '["memory:read","memory:write"]')"; then
    fail "memory: the human cannot issue a founder token"
    return 0
  fi
  if ! router="$(human_token router '["memory:read"]')"; then
    fail "memory: the human cannot issue a router token"
    return 0
  fi

  human_json "without signing in nobody reads memory" 401 '.status == 401' \
    curl -sS -m 10 "${DOOR}/v1/memory/search?q=${common}"
  human_json "a human cannot write memory (only agents do)" 403 '.status == 403' \
    from_browser POST /v1/memory -d "$(jq -nc '{scope: "common", title: "by a human", body: "not allowed"}')"
  id_common="$(human_as_agent "$founder" POST /memory \
    -d "$(jq -nc --arg b "the disk note ${common} for every agent" '{scope: "common", title: "E2E common note", body: $b, tags: ["e2e"]}')" |
    jq -er 'select(.scope == "common" and .owner == "founder") | .id')" || id_common=""
  if [ -n "$id_common" ]; then
    pass "the founder writes a common record ($id_common)"
  else
    fail "the founder writes a common record"
    return 0
  fi
  id_private="$(human_as_agent "$founder" POST /memory \
    -d "$(jq -nc --arg b "the private note ${private}" '{scope: "private", title: "E2E private note", body: $b}')" |
    jq -er 'select(.scope == "private" and .owner == "founder") | .id')" || id_private=""
  if [ -n "$id_private" ]; then
    pass "the founder writes a private record ($id_private)"
  else
    fail "the founder writes a private record"
    return 0
  fi

  human_json "search finds the common record for the founder" 200 \
    "[.items[] | select(.record.id == \"${id_common}\")] | length == 1" \
    human_as_agent "$founder" GET "/memory/search?q=${common}"
  human_json "search finds the common record for the human" 200 \
    "[.items[] | select(.record.id == \"${id_common}\" and (.record.body | contains(\"${common}\")))] | length == 1" \
    from_browser GET "/v1/memory/search?q=${common}"
  human_json "search finds the common record for the router below" 200 \
    "[.items[] | select(.record.id == \"${id_common}\")] | length == 1" \
    human_as_agent "$router" GET "/memory/search?q=${common}"
  human_json "the human reads every scope: search finds the private record" 200 \
    "[.items[] | select(.record.id == \"${id_private}\")] | length == 1" \
    from_browser GET "/v1/memory/search?q=${private}"
  human_json "search does not show the router the founder's private record" 200 \
    '.items | length == 0' \
    human_as_agent "$router" GET "/memory/search?q=${private}"
  human_json "the router cannot open the founder's private record by id" 404 '.status == 404' \
    human_as_agent "$router" GET "/memory/${id_private}"
  human_json "a word nobody wrote finds nothing" 200 '.items | length == 0' \
    from_browser GET "/v1/memory/search?q=zqnothing${run}"
  human_json "a read-only token cannot write memory" 403 '.status == 403' \
    human_as_agent "$router" POST /memory -d "$(jq -nc '{scope: "private", title: "no", body: "no"}')"

  human_json "the owner deletes the common record" 204 '' human_as_agent "$founder" DELETE "/memory/${id_common}"
  human_json "after the delete search no longer finds it" 200 '.items | length == 0' \
    from_browser GET "/v1/memory/search?q=${common}"
  human_json "the owner deletes the private record" 204 '' human_as_agent "$founder" DELETE "/memory/${id_private}"
}
