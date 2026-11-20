# SPDX-License-Identifier: MIT
# shellcheck shell=bash
#
# Hire: the founder hires an agent below itself, by a scripted tool call. The fake provider tells
# the founder's runner to call hire_agent; the runner calls it on the machine's tool server with
# the founder's own token, and the answer goes back to the model. The same turn holds a hire the
# chart refuses (a manager that is not in the society), which must leave nothing behind. This
# stage runs after the founder-turn stage: it needs the provider, the first human and the
# founder's runner that stage proves, and it reuses that stage's scripted-turn helpers.

stage hire "the founder hires an agent below itself through a scripted tool call"

# hire_final <name>: the founder's final text in the scripted turn.
hire_final() { printf 'The founder hired %s and the other hire was refused.' "$1"; }

# hire_script <hired name> <refused name>: the fake's script: one reply with two hire_agent calls
# (the second is refused), then the final text.
hire_script() {
  jq -nc --arg hired "$1" --arg refused "$2" --arg final "$(hire_final "$1")" '
    def body($name; $manager):
      {name: $name, display_name: ("E2E " + $name), kind: "chat", runner: "echo",
       role: "Writes down what the e2e run asks for", is_root: false}
      + (if $manager == "" then {} else {manager: $manager} end);
    {replies: [
      {tool_calls: [
        {id: "call_e2e_hire_ok", type: "function",
         function: {name: "hire_agent", arguments: (body($hired; "") | tojson)}},
        {id: "call_e2e_hire_refused", type: "function",
         function: {name: "hire_agent", arguments: (body($refused; "nobody-in-this-society") | tojson)}}]},
      {content: $final}]}'
}

# hire_agent_listed <name>: the agent list a human reads has the agent, a custom agent below the
# founder, hired by the founder, and not root.
hire_agent_listed() {
  from_browser GET '/v1/agents?limit=200' | jq -e --arg n "$1" '
    [.items[] | select(.name == $n)] as $a
    | ($a | length) == 1 and ($a[0] | .manager == "founder" and .is_root == false
        and .created_by == "agent:founder" and .runner == "echo" and .kind == "chat")'
}

# hire_agent_absent <name>: the agent list has no agent of this name.
hire_agent_absent() {
  from_browser GET '/v1/agents?limit=200' | jq -e --arg n "$1" 'all(.items[]; .name != $n)'
}

# hire_events <name> <count>: exactly <count> agentsone.agents.hired events are about the agent,
# each by the founder.
hire_events() {
  from_browser GET '/v1/events?type=agentsone.agents.hired&limit=200' | jq -e --arg n "$1" --argjson c "$2" '
    [.items[] | select(.data.agent == $n)] as $e
    | ($e | length) == $c and all($e[]; .actor == "agent:founder")'
}

# hire_audit_log: every audit record, as one JSON array, read page by page as the human (the audit
# list takes no filters yet).
hire_audit_log() {
  local after=0 page n all='[]'
  while :; do
    page="$(from_browser GET "/v1/audit?limit=200&after_seq=${after}")" || return 1
    n="$(jq -e '.items | length' <<<"$page")" || {
      printf '%s\n' "$page"
      return 1
    }
    [ "$n" -gt 0 ] || break
    all="$(jq -c --argjson page "$page" '. + $page.items' <<<"$all")" || return 1
    after="$(jq -e '.items[-1].seq' <<<"$page")" || return 1
  done
  printf '%s\n' "$all"
}

# hire_audits <name> <count>: exactly <count> agent.hire audit records are about the agent, each
# by agent:founder.
hire_audits() {
  hire_audit_log | jq -e --arg n "$1" --argjson c "$2" '
    [.[] | select(.action == "agent.hire" and .target.id == $n)] as $r
    | ($r | length) == $c and all($r[]; .actor.kind == "agent" and .actor.name == "founder")'
}

stage_hire() {
  needs hire machine edge fakeprovider || return 0
  door || return 0
  if [ ! -s "$WORK/login.json" ] || [ ! -s "$WORK/provider-key" ]; then
    fail "hire: no first human or no provider key; run the setup-page and model-turn stages first"
    return 0
  fi
  rm -f "$WORK/cookies" "$WORK/hire-assignment.json"
  expect_http "the first human signs in through the web door" '201' "\"name\":\"${HUMAN}\"" \
    from_browser POST /v1/session -d @"$WORK/login.json"

  local hired refused final script calls spent proofs out
  hired="scribe$(openssl rand -hex 3)"
  refused="refused$(openssl rand -hex 3)"
  final="$(hire_final "$hired")"
  script="$(hire_script "$hired" "$refused")"
  if ! { calls="$(fake_calls)" && spent="$(budget_field spent_micros)" && proofs="$(proof_count founder)"; }; then
    fail "hire: read the fake provider, the budget and the proof log before the turn"
    return 0
  fi
  expect ok "the agent list holds no agent named ${hired} before the hire" hire_agent_absent "$hired"
  expect_http "the fake provider takes the script of the hire turn: two hire_agent calls, then a text" \
    '204' '' founder_turn_put_script "$script"
  expect_http "a human request through the web door is routed again" '201' '' \
    from_browser POST /v1/router/route -d "$(jq -nc --arg t "$ROUTE_TEXT" '{text: $t}')" -o "$WORK/hire-assignment.json"
  expect_out "the router hands the request to the founder" '^true$' \
    jq -e '.chosen == "founder"' "$WORK/hire-assignment.json"

  if out="$(wait_for 60 founder_turn_reply "$final")"; then
    pass "the founder's final answer, after its hire calls, is stored in its chat: \"${final}\""
  else
    fail "the founder's final answer, after its hire calls, is stored in its chat" \
      "$(from_browser GET /v1/agents/founder/chat 2>&1) ${out}"
    return 0
  fi
  expect ok "the founder's turn used the whole script" founder_turn_script_used

  local turn
  if ! turn="$(founder_turn_calls "$calls")"; then
    fail "hire: read the fake provider's notes of the turn"
    return 0
  fi
  expect_out "the founder's turn made two scripted model calls" '^true$' \
    jq -e 'length == 2' <<<"$turn"
  expect_out "the founder's runner offered the model its tools from the machine, hire_agent among them" \
    '^true$' jq -e '.[0].tools | index("hire_agent") != null' <<<"$turn"
  # shellcheck disable=SC2016 # $c and $n are jq variables.
  expect_out "the hire_agent call reached the machine and its answer came back to the model: the new agent, hired by the founder" \
    '^true$' jq -e --arg c "call_e2e_hire_ok" --arg n "$hired" '
      [.[1].tool_results[] | select(.tool_call_id == $c) | .content | fromjson? // empty]
      | length == 1 and .[0].name == $n and .[0].manager == "founder" and .[0].is_root == false
        and .[0].created_by == "agent:founder"' <<<"$turn"
  expect ok "the new agent is in the agent list a human reads: a custom agent below the founder, not root" \
    hire_agent_listed "$hired"
  expect ok "one agentsone.agents.hired event is about the new agent, by agent:founder" \
    wait_for 60 hire_events "$hired" 1
  expect ok "the audit log has one agent.hire for the new agent, by agent:founder" \
    wait_for 60 hire_audits "$hired" 1

  # The refused hire: the manager is not in the society. The model gets an error, nothing stays.
  # shellcheck disable=SC2016 # $c is a jq variable.
  expect_out "the refused hire_agent call came back to the model as an error, with no agent made" \
    '^true$' jq -e --arg c "call_e2e_hire_refused" '
      [.[1].tool_results[] | select(.tool_call_id == $c) | .content]
      | length == 1 and (.[0] | test("status 4[0-9][0-9]"))' <<<"$turn"
  expect ok "the refused hire left no agent in the agent list" hire_agent_absent "$refused"
  expect ok "the refused hire made no hired event" hire_events "$refused" 0
  expect ok "the refused hire made no audit record" hire_audits "$refused" 0

  expect ok "the founder's calls reached the fake provider with the provider key, never the founder's token" \
    fake_saw_only_the_key "$calls" chat
  expect ok "the edge proof log has both of the founder's model calls: success, the fake's model" \
    founder_turn_proofs_grew "$proofs" 2
  expect ok "the budget spent grew by at least the two calls ($((2 * FAKE_COST)) micro-dollars)" \
    spent_grew "$spent" "$((2 * FAKE_COST))"
}
