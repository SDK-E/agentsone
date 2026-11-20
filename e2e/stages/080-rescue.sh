# SPDX-License-Identifier: MIT
# shellcheck shell=bash
#
# Rescue: a restart keeps the container layer, a recreate resets it to the image.

stage rescue "a restart keeps the machine; a recreate resets it to the image and keeps the volumes"

stage_rescue() {
  needs rescue machine || return 0
  local mark="agentsone-e2e-rescue-$$"
  expect ok "write a marker in /home (a volume) and in /opt (the container layer)" \
    in_machine sh -c "echo $mark > /home/.$mark && echo $mark > /opt/$mark"
  expect ok "restart the machine, as after a crash" \
    sh -c 'docker compose restart machine && docker compose up -d --no-deps --wait --wait-timeout 300 machine'
  expect ok "after a restart both markers are still there" \
    in_machine sh -c "[ -f /home/.$mark ] && [ -f /opt/$mark ]"
  expect ok "rescue: recreate the machine" \
    compose up -d --no-deps --force-recreate --wait --wait-timeout 300 machine
  expect ok "after the rescue the /home marker is kept" in_machine test -f "/home/.$mark"
  expect fail "after the rescue the /opt marker is gone" in_machine test -e "/opt/$mark"
  in_machine rm -f "/home/.$mark"
}
