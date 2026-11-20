# SPDX-License-Identifier: MIT
# shellcheck shell=bash
#
# Network wall: the outer network, where control sits, cannot reach inside.

stage wall-outer "the outer network cannot reach the machine or the edge's inner side"

stage_wall_outer() {
  needs wall-outer edge || return 0
  local machine_ip
  machine_ip="$(docker inspect -f "{{(index .NetworkSettings.Networks \"$(compose config --format json | jq -r '.networks.inner.name')\").IPAddress}}" "$(compose ps -q machine)" 2>/dev/null)"
  expect fail "outer network cannot reach the edge inner address" \
    from_outer -sS -m 5 -o /dev/null "telnet://${EDGE_IP}:53"
  expect fail "outer network cannot reach the edge DNS on its outer address" \
    from_outer -sS -m 5 -o /dev/null "telnet://${EDGE_OUTER_IP}:53"
  expect fail "outer network cannot reach the model gateway" \
    from_outer -sS -m 5 -o /dev/null "http://${EDGE_OUTER_IP}:4000/"
  if [ -n "$machine_ip" ]; then
    expect fail "outer network cannot reach the machine (${machine_ip}:7700)" \
      from_outer -sS -m 5 -o /dev/null "http://${machine_ip}:7700/"
  else
    fail "outer network cannot reach the machine: machine address not found"
  fi
}
