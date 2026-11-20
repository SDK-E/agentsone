# SPDX-License-Identifier: MIT
# shellcheck shell=bash
#
# Defence slice: a share of the total budget kept for defence calls (wakers and security work),
# which a human can switch off. Past the total, nothing pays. Needs the provider the model-turn
# stage sets up; leaves the budget and the slice as it found them.

stage defence-slice "with the rest of the budget spent, the defence slice still pays for defence calls"

# defence_slice_set <json>: a human changes the defence slice through the admin socket.
defence_slice_set() { printf '%s' "$1" | as_control_in /v1/gateway/budget/defence -X PATCH; }

# defence_slice_view <token>: the budget as the caller with this token sees it, from the machine.
defence_slice_view() {
  in_machine curl -sS -m 10 -H "Authorization: Bearer $1" "http://${EDGE_IP}:4000/v1/budget"
}

# defence_slice_left: what is left of the defence slice, through the admin socket.
defence_slice_left() { as_control /v1/gateway/budget | jq -e '.defence.remaining_micros | numbers'; }

# defence_slice_paid <left before> <at least>: the slice has at least <at least> micro-dollars less
# left than before.
defence_slice_paid() {
  local now
  now="$(defence_slice_left)" || return 1
  echo "defence slice left ${1} then ${now}"
  [ $(($1 - now)) -ge "$2" ]
}

stage_defence_slice() {
  needs defence-slice machine edge fakeprovider || return 0
  if [ ! -s "$WORK/provider-key" ]; then
    fail "defence-slice: no provider is set up; run the model-turn stage first"
    return 0
  fi
  local token defence_token total spent slice calls proofs left
  if ! { token="$(signed_token)" && [ -n "$token" ] && defence_token="$(signed_token defence)" && [ -n "$defence_token" ]; }; then
    fail "defence-slice: sign gateway tokens in the machine"
    return 0
  fi
  if ! { total="$(budget_field total_micros)" && spent="$(budget_field spent_micros)" &&
    slice="$(as_control /v1/gateway/budget | jq -ce '.defence | {enabled, share_pct}')"; }; then
    fail "defence-slice: read the budget"
    return 0
  fi

  # The slice is the whole total, and the total is 0.1 USD past what is spent: what is left is
  # all the slice's. A total in whole hundreds of micro-dollars makes the slice exactly the total.
  expect_http "a human keeps the whole total for the defence slice" '200' '"defence":\{"enabled":true,"share_pct":100,' \
    defence_slice_set '{"enabled":true,"share_pct":100}'
  expect_http "a human sets the total 0.1 USD past what is spent: the rest of the budget is used up" '200' \
    '"exhausted":true' set_budget_total "$(((spent + 100000 + 99) / 100 * 100))"
  expect_http "an ordinary caller sees the budget used up" '200' '"defence":false,"exhausted":true' \
    defence_slice_view "$token"
  expect_http "a defence caller sees budget left" '200' '"defence":true,"exhausted":false' \
    defence_slice_view "$defence_token"
  if ! { calls="$(fake_calls)" && proofs="$(proof_count system:e2e)" && left="$(defence_slice_left)"; }; then
    fail "defence-slice: read the fake provider, the proof log and the slice"
    return 0
  fi
  expect_http "an ordinary model call is refused" '402' 'urn:agentsone:problem:budget-exhausted' model_call "$token"
  expect_out "the refused call reached no provider" "^${calls}\$" fake_calls
  expect_proof "the edge proof log has the refused call as denied (402)" system:e2e "$proofs" \
    '.outcome == "denied" and .details.status == 402 and .details.cost_micros == 0'
  expect_http "a defence model call is answered by the fake provider: the slice pays" '200' \
    "\"content\":\"${FAKE_CHAT_REPLY}\"" model_call "$defence_token"
  expect ok "the defence call reached the fake provider with the provider key only" \
    fake_saw_only_the_key "$calls" chat
  expect_proof "the edge proof log has the defence call: status 200, the fake's cost" system:e2e "$((proofs + 1))" \
    ".outcome == \"success\" and .details.status == 200 and .details.cost_micros == ${FAKE_COST}"
  expect ok "the defence slice paid for the call (${FAKE_COST} micro-dollars)" defence_slice_paid "$left" "$FAKE_COST"

  # Switched off, the slice keeps nothing back, and past the total a defence call is refused too.
  expect_http "a human switches the defence slice off" '200' '"defence":\{"enabled":false,' \
    defence_slice_set '{"enabled":false}'
  if ! { spent="$(budget_field spent_micros)" && calls="$(fake_calls)"; }; then
    fail "defence-slice: read the budget and the fake provider"
    return 0
  fi
  expect_http "a human sets the total to what is spent" '200' '"exhausted":true' set_budget_total "$spent"
  expect_http "slice off, total used up: a defence model call is refused too" '402' \
    'urn:agentsone:problem:budget-exhausted' model_call "$defence_token"
  expect_out "the refused defence call reached no provider" "^${calls}\$" fake_calls

  expect_http "a human puts the defence slice back as it was" '200' '"defence":\{"enabled":' defence_slice_set "$slice"
  expect_http "a human puts the total back" '200' '"exhausted":false' set_budget_total "$total"
}
