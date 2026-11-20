# SPDX-License-Identifier: MIT
# shellcheck shell=bash
#
# Freeze all: a hard wall in the edge, switched only through the admin socket.

stage freeze-all "freeze all cuts model calls and egress, holds over an edge restart, and releases"

stage_freeze_all() {
  needs freeze-all machine edge || return 0
  local token
  if ! token="$(signed_token)" || [ -z "$token" ]; then
    fail "freeze-all: sign a gateway token in the machine" "$token"
    return 0
  fi
  expect_http "freeze all is off at the start" '200' '"frozen":false' as_control /v1/freeze
  # No provider is set up here, so an unfrozen call ends at routing (503), past the freeze gate.
  expect_http "before the freeze a signed model call passes the freeze gate" '(200|402|502|503)' '' model_call "$token"
  expect_http "a human freezes all through the admin socket" '200' '"frozen":true' as_control /v1/freeze -X POST
  expect_http "frozen: a model call is refused" '423' 'urn:agentsone:problem:frozen' model_call "$token"
  expect fail "frozen: the machine cannot reach an allowed site (https://github.com)" \
    in_machine curl -sS -m 10 -o /dev/null https://github.com
  expect ok "restart the edge" \
    sh -c 'docker compose restart edge && docker compose up -d --no-deps --wait --wait-timeout 300 edge'
  expect_http "after an edge restart freeze all is still on" '200' '"frozen":true' as_control /v1/freeze
  expect_http "after an edge restart a model call is still refused" '423' 'urn:agentsone:problem:frozen' model_call "$token"
  expect fail "after an edge restart the machine still cannot reach https://github.com" \
    in_machine curl -sS -m 10 -o /dev/null https://github.com
  expect_http "a human releases the freeze" '200' '"frozen":false' as_control /v1/freeze -X DELETE
  expect_http "released: a model call passes the freeze gate again" '(200|402|502|503)' '' model_call "$token"
  expect_out "released: the machine reaches https://github.com again" '^[23][0-9][0-9]$' \
    in_machine curl -sS -m 30 -o /dev/null -w '%{http_code}' https://github.com
  expect_http "the edge proof log records the freeze and the release" '200' '"freeze\.on".*"freeze\.off"' \
    as_control '/v1/audit/proof?limit=1000'
}
