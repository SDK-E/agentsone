# SPDX-License-Identifier: MIT
# shellcheck shell=bash
#
# Network wall: the machine has no way out of its own.

stage wall-route "the machine has no route out and no published port"

stage_wall_route() {
  needs wall-route machine || return 0
  expect fail "machine has no IPv4 default route" \
    in_machine sh -c "awk 'NR > 1 && \$2 == \"00000000\" { found = 1 } END { exit !found }' /proc/net/route"
  expect fail "machine has no IPv6 default route" \
    in_machine sh -c "[ -r /proc/net/ipv6_route ] && awk '\$1 == \"00000000000000000000000000000000\" && \$2 == \"00\" && \$10 != \"lo\" { found = 1 } END { exit !found }' /proc/net/ipv6_route"
  expect_out "machine has one network interface besides loopback" '^1$' \
    in_machine sh -c 'ls /sys/class/net | grep -vx lo | wc -l | tr -d " "'
  expect ok "machine has no published ports" sh -c "[ -z \"\$(docker port \"\$(docker compose ps -q machine)\")\" ]"
}
