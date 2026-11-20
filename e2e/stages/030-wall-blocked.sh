# SPDX-License-Identifier: MIT
# shellcheck shell=bash
#
# Network wall: the allow list blocks a name that is not on it, whatever the machine forges.

stage wall-blocked "the allow list blocks a name that is not on it"

stage_wall_blocked() {
  needs wall-blocked machine || return 0
  expect fail "blocked name example.com does not resolve" in_machine getent hosts example.com
  expect fail "blocked: curl https://example.com" in_machine curl -sS -m 10 -o /dev/null https://example.com
  needs wall-blocked edge || return 0
  expect fail "blocked: forged SNI example.com to the edge :443" \
    in_machine curl -sS -m 10 -o /dev/null --resolve "example.com:443:${EDGE_IP}" https://example.com
  expect_out "blocked: Host example.com to the edge :80 is refused (403)" '^403$' \
    in_machine curl -sS -m 10 -o /dev/null -w '%{http_code}' -H 'Host: example.com' "http://${EDGE_IP}/"
}
