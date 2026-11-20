# SPDX-License-Identifier: MIT
# shellcheck shell=bash
#
# Founder turn: the founder answers a routed chat with its own model calls, through its runner
# and the gateway to the e2e fake provider. First the answer to the request the model-turn stage
# routed (the fake's fixed reply). Then a scripted turn: the fake asks for two tool calls, the
# runner runs them on the machine's tool server as the founder, the tool results go back to the
# model, and the founder's final text is stored in its chat. Needs the model-turn stage first.

stage founder-turn "the founder answers a routed chat through the gateway, and its tool calls reach the machine and come back"

# The founder's final answer in the scripted turn, and the ids of the tool calls the fake asks for.
FOUNDER_TURN_FINAL="The founder listed the agents and opened the e2e task."
FOUNDER_TURN_AGENTS_CALL="call_e2e_list_agents"
FOUNDER_TURN_TASK_CALL="call_e2e_create_task"

# founder_turn_reply <text>: the founder's chat, read as the signed-in human through the web door,
# holds a reply by the founder with exactly this text.
founder_turn_reply() {
  from_browser GET /v1/agents/founder/chat | jq -e --arg t "$1" \
    'any(.items[]; .role == "agent" and .author == "agent:founder" and .text == $t)'
}

# founder_turn_script <task title>: the fake's script for the founder's turn: first the two tool
# calls, then the final text.
founder_turn_script() {
  jq -nc --arg title "$1" --arg final "$FOUNDER_TURN_FINAL" \
    --arg agents "$FOUNDER_TURN_AGENTS_CALL" --arg task "$FOUNDER_TURN_TASK_CALL" '{replies: [
      {tool_calls: [
        {id: $agents, type: "function", function: {name: "list_agents", arguments: "{}"}},
        {id: $task, type: "function", function: {name: "create_task", arguments: ({title: $title} | tojson)}}]},
      {content: $final}]}'
}

# founder_turn_put_script <json> [curl args...]: PUT the script on the fake provider, from the
# outer network.
founder_turn_put_script() {
  local body="$1"
  shift
  from_outer -sS -m 10 -X PUT -H 'Content-Type: application/json' --data-binary "$body" "$@" \
    "http://fakeprovider:8080/script"
}

# founder_turn_script_used: the fake provider has no scripted reply left.
founder_turn_script_used() { from_outer -sS -m 10 "http://fakeprovider:8080/script" | jq -e '.left == 0'; }

# founder_turn_calls <from>: the scripted calls after the first <from> notes, as one JSON array.
founder_turn_calls() { fake_seen | jq -ce --argjson from "$1" '[.calls[$from:][] | select(.scripted)]'; }

# founder_turn_task <title>: the task list, read as the human, has the task, made by the founder.
founder_turn_task() {
  from_browser GET '/v1/tasks?limit=200' | jq -e --arg t "$1" \
    'any(.items[]; .title == $t and .created_by == "agent:founder")'
}

# founder_turn_proofs_grew <before> <at least>: the edge proof log has at least <at least> more
# successful model calls by the founder than <before>, each answered by the fake provider.
founder_turn_proofs_grew() {
  local lines
  lines="$(proof_calls founder)" || return 1
  printf '%s\n' "$lines" | grep '^{' | jq -se --argjson before "$1" --argjson n "$2" --arg p "${FAKE_PROVIDER}/" '
    .[$before:] as $new
    | ($new | length) >= $n
      and all($new[]; .outcome == "success" and .details.status == 200 and (.details.model | startswith($p)))'
}

stage_founder_turn() {
  needs founder-turn machine edge fakeprovider || return 0
  door || return 0
  if [ ! -s "$WORK/login.json" ] || [ ! -s "$WORK/provider-key" ]; then
    fail "founder-turn: no first human or no provider key; run the setup-page and model-turn stages first"
    return 0
  fi
  rm -f "$WORK/cookies" "$WORK/founder-assignment.json"
  expect_http "the first human signs in through the web door" '201' "\"name\":\"${HUMAN}\"" \
    from_browser POST /v1/session -d @"$WORK/login.json"

  # The routed chat of the model-turn stage: the founder's runner answers it through the gateway
  # with the fake provider's fixed reply.
  local out
  if out="$(wait_for 60 founder_turn_reply "$FAKE_CHAT_REPLY")"; then
    pass "the founder answered the routed chat through the gateway: its reply is the fake provider's answer"
  else
    fail "the founder answered the routed chat through the gateway: its reply is the fake provider's answer" \
      "$(from_browser GET /v1/agents/founder/chat 2>&1) ${out}"
    return 0
  fi
  expect ok "the founder's model call carried the edge proof log: success, the fake's model" \
    founder_turn_proofs_grew 0 1

  # A scripted turn: the founder calls two tools on the machine, then answers.
  local title calls spent proofs script
  title="e2e founder task $(openssl rand -hex 4)"
  script="$(founder_turn_script "$title")"
  if ! { calls="$(fake_calls)" && spent="$(budget_field spent_micros)" && proofs="$(proof_count founder)"; }; then
    fail "founder-turn: read the fake provider, the budget and the proof log before the turn"
    return 0
  fi
  expect_http "the fake provider takes the script of the founder's turn: two tool calls, then a text" \
    '204' '' founder_turn_put_script "$script"
  expect_http "a human request through the web door is routed again" '201' '' \
    from_browser POST /v1/router/route -d "$(jq -nc --arg t "$ROUTE_TEXT" '{text: $t}')" -o "$WORK/founder-assignment.json"
  expect_out "the router hands the request to the founder" '^true$' \
    jq -e '.chosen == "founder"' "$WORK/founder-assignment.json"

  if out="$(wait_for 60 founder_turn_reply "$FOUNDER_TURN_FINAL")"; then
    pass "the founder's final answer, after its tool calls, is stored in its chat: \"${FOUNDER_TURN_FINAL}\""
  else
    fail "the founder's final answer, after its tool calls, is stored in its chat" \
      "$(from_browser GET /v1/agents/founder/chat 2>&1) ${out}"
    return 0
  fi
  expect ok "the founder's turn used the whole script" founder_turn_script_used

  local turn
  if ! turn="$(founder_turn_calls "$calls")"; then
    fail "founder-turn: read the fake provider's notes of the turn"
    return 0
  fi
  expect_out "the founder's turn made two scripted model calls" '^true$' \
    jq -e 'length == 2' <<<"$turn"
  expect_out "the founder's runner offered the model its tools from the machine, list_agents and create_task among them" \
    '^true$' jq -e '.[0].tools | index("list_agents") != null and index("create_task") != null' <<<"$turn"
  # shellcheck disable=SC2016 # $a is a jq variable.
  expect_out "the list_agents call reached the machine and its answer came back to the model: it names the founder, router and waker" \
    '^true$' jq -e --arg a "$FOUNDER_TURN_AGENTS_CALL" '
      [.[1].tool_results[] | select(.tool_call_id == $a) | .content | fromjson? // empty]
      | length == 1 and ([.[0].items[].name] | index("founder") != null and index("router") != null and index("waker") != null)' <<<"$turn"
  # shellcheck disable=SC2016 # $c and $t are jq variables.
  expect_out "the create_task call reached the machine and its answer came back to the model: the new task, made by the founder" \
    '^true$' jq -e --arg c "$FOUNDER_TURN_TASK_CALL" --arg t "$title" '
      [.[1].tool_results[] | select(.tool_call_id == $c) | .content | fromjson? // empty]
      | length == 1 and .[0].title == $t and .[0].created_by == "agent:founder" and (.[0].id | startswith("tsk_"))' <<<"$turn"
  expect ok "the task the founder made is in the task list a human reads" founder_turn_task "$title"
  expect ok "the founder's calls reached the fake provider with the provider key, never the founder's token" \
    fake_saw_only_the_key "$calls" chat
  expect ok "the edge proof log has both of the founder's model calls: success, the fake's model" \
    founder_turn_proofs_grew "$proofs" 2
  expect ok "the budget spent grew by at least the two calls ($((2 * FAKE_COST)) micro-dollars)" \
    spent_grew "$spent" "$((2 * FAKE_COST))"
}
