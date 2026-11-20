# SPDX-License-Identifier: MIT
# shellcheck shell=bash

stage bus "a human and an agent exchange messages on the bus, and an agent below sees only what it may"

# The agents are tokens the human issued (no runner needs to answer): the founder, who leads the
# society, and the router, who sits below it.
stage_bus() {
  needs bus machine edge || return 0
  door || return 0
  human_signin bus || return 0
  local run channel dm reply founder router out
  run="$(openssl rand -hex 4)"
  channel="e2e-bus-${run}"
  dm="hello founder ${run}"
  reply="report from founder ${run}"
  if ! out="$(wait_for 60 human_agent_present founder)"; then
    fail "bus: the founder is not there 60 seconds after setup" "$out"
    return 0
  fi
  if ! founder="$(human_token founder '["bus:read","bus:write"]')"; then
    fail "bus: the human cannot issue a founder token"
    return 0
  fi
  if ! router="$(human_token router '["bus:read"]')"; then
    fail "bus: the human cannot issue a router token"
    return 0
  fi

  human_json "without signing in nobody reads the bus" 401 '.status == 401' \
    curl -sS -m 10 "${DOOR}/v1/bus/messages"
  human_json "a human creates a public channel" 201 ".name == \"${channel}\" and .kind == \"public\"" \
    from_browser POST /v1/bus/channels -d "$(jq -nc --arg n "$channel" '{name: $n, kind: "public", description: "e2e bus stage"}')"
  human_json "a human sends the founder a direct message" 201 \
    ".to == \"founder\" and .text == \"${dm}\" and .from == \"human:${HUMAN}\"" \
    from_browser POST /v1/bus/messages -d "$(jq -nc --arg t "$dm" '{to: "founder", text: $t}')"
  human_json "the founder finds the human's message in its inbox" 200 \
    "[.items[] | select(.text == \"${dm}\" and .from == \"human:${HUMAN}\" and .to == \"founder\")] | length == 1" \
    human_as_agent "$founder" GET "/bus/messages?to=founder"
  human_json "the founder answers on the channel" 201 \
    ".channel == \"${channel}\" and .text == \"${reply}\" and .from == \"agent:founder\"" \
    human_as_agent "$founder" POST /bus/messages -d "$(jq -nc --arg c "$channel" --arg t "$reply" '{channel: $c, text: $t}')"
  human_json "the human reads the founder's answer on the channel" 200 \
    "[.items[] | select(.text == \"${reply}\" and .from == \"agent:founder\")] | length == 1" \
    from_browser GET "/v1/bus/messages?channel=${channel}"
  human_json "the router below the founder cannot read the founder's inbox" 200 \
    "[.items[] | select(.text == \"${dm}\")] | length == 0" \
    human_as_agent "$router" GET "/bus/messages?to=founder"
  human_json "the router reads the public channel" 200 \
    "[.items[] | select(.text == \"${reply}\")] | length == 1" \
    human_as_agent "$router" GET "/bus/messages?channel=${channel}"
  human_json "a token that may only read cannot send" 403 '.status == 403' \
    human_as_agent "$router" POST /bus/messages -d "$(jq -nc --arg c "$channel" '{channel: $c, text: "not allowed"}')"
}
