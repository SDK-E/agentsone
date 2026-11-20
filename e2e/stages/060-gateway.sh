# SPDX-License-Identifier: MIT
# shellcheck shell=bash
#
# Model gateway: the machine reaches it on the inner address, with a token only.

stage gateway "the machine reaches the model gateway with a token only"

stage_gateway() {
  needs gateway machine edge || return 0
  local base="http://${EDGE_IP}:4000"
  expect_out "machine reaches the model gateway; no token is refused (401)" '^401$' \
    in_machine curl -sS -m 5 -o /dev/null -w '%{http_code}' "${base}/v1/models"
  expect_out "a forged token is refused (401)" '^401$' \
    in_machine curl -sS -m 5 -o /dev/null -w '%{http_code}' -X POST -H 'Authorization: Bearer forged' \
    -H 'Content-Type: application/json' -d '{"model":"autoagents/one"}' "${base}/v1/chat/completions"
  expect_out "the gateway serves no admin route to the machine (404)" '^404$' \
    in_machine curl -sS -m 5 -o /dev/null -w '%{http_code}' "${base}/v1/gateway/budget"
  expect_out "the machine cannot change the budget through the gateway (404 or 405)" '^40[45]$' \
    in_machine curl -sS -m 5 -o /dev/null -w '%{http_code}' -X PATCH -d '{}' "${base}/v1/budget"
}
