# SPDX-License-Identifier: MIT
# shellcheck shell=bash
#
# Budget wall: the total is a hard wall in the edge; past it nothing reaches a provider. Needs the
# provider the model-turn stage sets up.

stage budget-wall "past the total budget no model call reaches a provider"

stage_budget_wall() {
  needs budget-wall machine edge fakeprovider || return 0
  if [ ! -s "$WORK/provider-key" ]; then
    fail "budget-wall: no provider is set up; run the model-turn stage first"
    return 0
  fi
  local token total spent calls proofs
  if ! token="$(signed_token)" || [ -z "$token" ]; then
    fail "budget-wall: sign a gateway token in the machine" "$token"
    return 0
  fi
  if ! { total="$(budget_field total_micros)" && spent="$(budget_field spent_micros)" && proofs="$(proof_count system:e2e)"; }; then
    fail "budget-wall: read the budget and the proof log"
    return 0
  fi
  expect_http "a human sets the total to what is already spent" '200' '"exhausted":true' set_budget_total "$spent"
  calls="$(fake_calls)" || {
    fail "budget-wall: read the fake provider"
    return 0
  }
  expect_http "budget used up: a model call is refused" '402' 'urn:agentsone:problem:budget-exhausted' model_call "$token"
  expect_out "budget used up: nothing reached the fake provider" "^${calls}\$" fake_calls
  expect_proof "the edge proof log has the refused call as denied (402)" system:e2e "$proofs" \
    '.outcome == "denied" and .details.status == 402 and .details.cost_micros == 0'
  expect_http "a human puts the total back" '200' '"exhausted":false' set_budget_total "$total"
  expect_http "budget back: a model call is answered by the fake provider again" '200' \
    "\"content\":\"${FAKE_CHAT_REPLY}\"" model_call "$token"
  expect ok "budget back: the call reached the fake provider with the provider key only" fake_saw_only_the_key "$calls" chat
}
