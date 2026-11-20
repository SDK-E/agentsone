# SPDX-License-Identifier: MIT
# shellcheck shell=bash
#
# Model turn: a real model call through the gateway, ending at the e2e fake provider. It sets the
# provider up (key in $WORK/provider-key) for the stages after it.

stage model-turn "a model call through the gateway reaches the provider with the provider key only"

stage_model_turn() {
  needs model-turn machine edge fakeprovider || return 0
  local fake_ip outer fp token calls spent proofs

  # The fake provider is outside the wall: the edge reaches it, the machine cannot.
  outer="$(compose config --format json | jq -r '.networks.outer.name')"
  fake_ip="$(docker inspect -f "{{(index .NetworkSettings.Networks \"${outer}\").IPAddress}}" "$(compose ps -q fakeprovider)" 2>/dev/null)"
  expect fail "the machine cannot resolve the fake provider" in_machine getent hosts fakeprovider
  if [ -n "$fake_ip" ]; then
    expect fail "the machine cannot reach the fake provider by address (${fake_ip}:8080)" \
      in_machine curl -sS -m 5 -o /dev/null "http://${fake_ip}:8080/healthz"
  else
    fail "model-turn: the fake provider's address is not found"
  fi
  expect_http "the outer network reaches the fake provider" '200' '"calls":' from_outer -sS -m 10 "$FAKE_SEEN_URL"

  # A human sets the provider up: its address in gateway.toml, the rest through the admin socket.
  (umask 077 && openssl rand -hex 24 >"$WORK/provider-key") || {
    fail "model-turn: make a provider key"
    return 0
  }
  # The edge fingerprint is the first 8 hex digits of the key's SHA-256 (adminapi.Fingerprint).
  fp="$(printf '%s' "$(cat "$WORK/provider-key")" | openssl dgst -sha256 -r | cut -c1-8)"
  expect ok "gateway.toml names the fake provider (written as the edge user, as a human edits it)" seed_gateway_config
  expect_http "a human switches the fake provider on through the admin socket" '200' \
    "\"id\":\"${FAKE_PROVIDER}\",\"enabled\":true,\"key_required\":true,\"key\":\\{\"set\":false\\}" \
    as_control "/v1/gateway/providers/${FAKE_PROVIDER}" -X PUT
  expect_http "a human sets a key made for this run; the edge answers with its fingerprint only" '200' \
    "^\\{\"set\":true,\"fingerprint\":\"${fp}\"\\}\$" \
    as_control_in "/v1/gateway/providers/${FAKE_PROVIDER}/key" -X PUT < <(jq -Rc '{key: .}' "$WORK/provider-key")
  expect_out "the provider list shows only the fake provider on, with the key's fingerprint" '^true$' \
    provider_listed "$fp"
  expect ok "the provider list never shows the key" key_never_shown
  expect_http "a human sets a total budget of 1 USD through the admin socket" '200' '"total_micros":1000000' \
    set_budget_total 1000000

  # The gateway leg: a call signed like the daemon's tokens, from the machine.
  if ! token="$(signed_token)" || [ -z "$token" ]; then
    fail "model-turn: sign a gateway token in the machine" "$token"
    return 0
  fi
  if ! { calls="$(fake_calls)" && spent="$(budget_field spent_micros)" && proofs="$(proof_count system:e2e)"; }; then
    fail "model-turn: read the fake provider, the budget and the proof log before the call"
    return 0
  fi
  expect_http "a model call from the machine is answered by the fake provider through the gateway" '200' \
    "\"content\":\"${FAKE_CHAT_REPLY}\"" model_call "$token"
  expect ok "the fake provider got the provider key, never the gateway token" fake_saw_only_the_key "$calls" chat
  expect_proof "the edge proof log has the call: system caller, the fake's model, its tokens and cost" \
    system:e2e "$proofs" \
    ".outcome == \"success\" and .details.kind == \"system\" and .details.status == 200 and
     (.details.model | startswith(\"${FAKE_PROVIDER}/\")) and .details.input_tokens == 1000 and
     .details.output_tokens == 50 and .details.cost_micros == ${FAKE_COST}"
  expect ok "the budget spent grew by at least the call's cost (${FAKE_COST} micro-dollars)" \
    spent_grew "$spent" "$FAKE_COST"

  # The router leg: a human request through the web door, rated by the router's own model call
  # with the token the daemon mints for system:router.
  door || return 0
  if [ ! -s "$WORK/login.json" ]; then
    fail "model-turn: no first human; run the setup-page stage first"
    return 0
  fi
  # The first human (the setup-page stage) makes the daemon install the Small Seed, router
  # included, in the background. Wait for setup to say so.
  local seed
  if seed="$(wait_for 60 seed_installed)"; then
    pass "the Small Seed is installed: the society has a router"
  else
    fail "the Small Seed is installed: the society has a router" "$seed"
    return 0
  fi
  if ! { calls="$(fake_calls)" && spent="$(budget_field spent_micros)" && proofs="$(proof_count system:router)"; }; then
    fail "model-turn: read the fake provider, the budget and the proof log before routing"
    return 0
  fi
  rm -f "$WORK/cookies" "$WORK/assignment.json"
  expect_http "the first human signs in through the web door" '201' "\"name\":\"${HUMAN}\"" \
    from_browser POST /v1/session -d @"$WORK/login.json"
  expect_http "a human request through the web door is routed to an agent's chat" '201' '' \
    from_browser POST /v1/router/route -d "$(jq -nc --arg t "$ROUTE_TEXT" '{text: $t}')" -o "$WORK/assignment.json"
  # shellcheck disable=SC2016 # $r is a jq variable.
  expect_out "the router's model call picked the agent: its pick carries the fake provider's reason" '^true$' \
    jq -e --arg r "$FAKE_ROUTE_REASON" '.chosen == .shortlist[0].agent and .shortlist[0].reason == $r' "$WORK/assignment.json"
  local chat_path message_id
  chat_path="$(jq -r '.chat_path // empty' "$WORK/assignment.json" 2>/dev/null)"
  message_id="$(jq -r '.chat_message_id // empty' "$WORK/assignment.json" 2>/dev/null)"
  if [ -n "$chat_path" ] && [ -n "$message_id" ]; then
    expect_http "the request is in the chosen agent's chat" '200' "\"id\":\"${message_id}\"" from_browser GET "$chat_path"
    expect_http "the chat message holds the human's text" '200' "\"text\":\"${ROUTE_TEXT}\"" from_browser GET "$chat_path"
  else
    fail "model-turn: the assignment names no chat message" "$(cat "$WORK/assignment.json" 2>/dev/null)"
  fi
  expect ok "the router's call reached the fake provider with the provider key, never the router's token" \
    fake_saw_only_the_key "$calls" route
  expect_proof "the edge proof log has the router's call: agent system:router, system kind, status 200" \
    system:router "$proofs" \
    ".outcome == \"success\" and .details.kind == \"system\" and .details.status == 200 and
     .details.model == \"${FAKE_PROVIDER}/fake-quick\" and .details.cost_micros == ${FAKE_COST}"
  expect ok "the budget spent grew by at least the router's call (${FAKE_COST} micro-dollars)" \
    spent_grew "$spent" "$FAKE_COST"
}
