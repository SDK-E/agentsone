# SPDX-License-Identifier: MIT
# shellcheck shell=bash
#
# Network wall: the allow list passes a name that is on it, through the edge.

stage wall-allowed "the allow list passes a name that is on it"

stage_wall_allowed() {
  needs wall-allowed machine edge || return 0
  expect_out "allowed name deb.debian.org resolves to the edge" "^${EDGE_IP//./\\.} " \
    in_machine getent ahostsv4 deb.debian.org
  expect_out "allowed: https://github.com through the edge (TLS by name)" '^[23][0-9][0-9]$' \
    in_machine curl -sS -m 30 -o /dev/null -w '%{http_code}' https://github.com
  expect_out "allowed: http://deb.debian.org through the edge (HTTP by Host)" '^[23][0-9][0-9]$' \
    in_machine curl -sS -m 30 -o /dev/null -w '%{http_code}' http://deb.debian.org/debian/
}
