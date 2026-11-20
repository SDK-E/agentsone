# SPDX-License-Identifier: MIT
# shellcheck shell=bash
#
# Network wall: no address is reachable directly, and the doors are not reachable from inside.

stage wall-direct "the machine reaches no address directly, nor the doors from inside"

stage_wall_direct() {
  needs wall-direct machine || return 0
  expect fail "machine cannot reach http://1.1.1.1 by address" in_machine curl -sS -m 5 http://1.1.1.1
  expect fail "machine cannot reach https://1.1.1.1 by address" in_machine curl -sS -m 5 https://1.1.1.1
  expect fail "machine cannot reach a public DNS server (1.1.1.1:53)" in_machine curl -sS -m 5 telnet://1.1.1.1:53
  expect fail "machine cannot reach the edge web door on the inner address" \
    in_machine curl -sS -m 5 -o /dev/null "http://${EDGE_IP}:8080/"
  expect fail "machine cannot reach the edge outer address" \
    in_machine curl -sS -m 5 -o /dev/null "http://${EDGE_OUTER_IP}:8080/"
  expect fail "machine cannot resolve the control service" in_machine getent hosts control
}
