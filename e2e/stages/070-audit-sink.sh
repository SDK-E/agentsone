# SPDX-License-Identifier: MIT
# shellcheck shell=bash
#
# Audit sink: only the daemon may send machine records.

stage audit-sink "only the daemon may send audit records to the edge"

stage_audit_sink() {
  needs audit-sink machine edge || return 0
  local key=/run/agentsone/gateway-key/key sink="http://${EDGE_IP}:7710/v1/audit/records"
  # An agent user's id (agents hold 200000 to 299999). Checks only whether the key is readable;
  # nothing here prints it.
  local as_agent=(setpriv --reuid=299999 --regid=299999 --clear-groups)
  expect ok "the gateway key is mounted in the machine" in_machine test -f "$key"
  expect ok "a command can run as an agent's user" in_machine "${as_agent[@]}" true
  expect fail "an agent's user cannot read the gateway key" in_machine "${as_agent[@]}" test -r "$key"
  expect_out "a record with no token is refused (401)" '^401$' \
    in_machine "${as_agent[@]}" curl -sS -m 5 -o /dev/null -w '%{http_code}' -X POST \
    -H 'Content-Type: application/json' -d '{"records":[{}]}' "$sink"
  expect_out "a record with a forged token is refused (401)" '^401$' \
    in_machine "${as_agent[@]}" curl -sS -m 5 -o /dev/null -w '%{http_code}' -X POST \
    -H 'Authorization: Bearer forged' -H 'Content-Type: application/json' -d '{"records":[{}]}' "$sink"
}
