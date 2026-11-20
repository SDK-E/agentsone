# SPDX-License-Identifier: MIT
# shellcheck shell=bash
#
# Small Seed: the default society installs on first boot. The setup-page stage made the first
# human; on that, the daemon installs the Small Seed in the background: a founder (root), a router
# and a waker below it. Leaves the society's agents in $WORK/agents.json.

stage small-seed "on first boot the Small Seed installs a founder, a router and a waker below it"

# small_seed_agent <name> <jq test>: exactly one agent of that name, and it passes the test.
small_seed_agent() {
  jq -e --arg n "$1" "[.items[] | select(.name == \$n)] | length == 1 and (.[0] | $2)" "$WORK/agents.json"
}

# small_seed_own_home <name>: the agent's uid is a Linux user in the machine, and its home is a
# folder only that user can open (mode 700, owned by the agent's uid).
small_seed_own_home() {
  local uid home mode
  uid="$(jq -er --arg n "$1" '.items[] | select(.name == $n) | .uid' "$WORK/agents.json")" || return 1
  home="$(jq -er --arg n "$1" '.items[] | select(.name == $n) | .home' "$WORK/agents.json")" || return 1
  in_machine getent passwd "$uid" || return 1
  mode="$(in_machine stat -c '%a %u' "$home")" || return 1
  echo "$home: mode and owner $mode"
  [ "$mode" = "700 $uid" ]
}

stage_small_seed() {
  needs small-seed machine edge || return 0
  door || return 0
  if [ ! -s "$WORK/login.json" ]; then
    fail "small-seed: no first human; run the setup-page stage first"
    return 0
  fi
  local seed
  if seed="$(wait_for 60 seed_installed)"; then
    pass "after the first human, setup says the Small Seed is installed"
  else
    fail "after the first human, setup says the Small Seed is installed" "$seed"
    return 0
  fi
  rm -f "$WORK/cookies" "$WORK/agents.json"
  expect_http "the first human signs in through the web door" '201' "\"name\":\"${HUMAN}\"" \
    from_browser POST /v1/session -d @"$WORK/login.json"
  expect_http "the human lists the society's agents" '200' '' from_browser GET /v1/agents -o "$WORK/agents.json"
  expect_out "the founder heads the chart: a root, always-on agent with no manager, uid 0, no limits" '^true$' \
    small_seed_agent founder '.is_root == true and .kind == "always-on" and .manager == null and .team == "founder" and
      .uid == 0 and .limits == null and .home == "/root/agents/founder"'
  expect_out "the router is a chat agent below the founder, not root" '^true$' \
    small_seed_agent router '.is_root == false and .kind == "chat" and .manager == "founder" and .team == "founder" and
      .uid > 0 and .home == "/home/agents/router"'
  expect_out "the waker is a waker below the founder, not root" '^true$' \
    small_seed_agent waker '.is_root == false and .kind == "waker" and .manager == "founder" and .team == "founder" and
      .uid > 0 and .home == "/home/agents/waker"'
  expect_out "no agent of the Small Seed has failed" '^true$' \
    jq -e '[.items[] | select(.name == "founder" or .name == "router" or .name == "waker")] | length == 3 and
      all(.[]; .state != "failed" and (.state_reason // null) == null)' "$WORK/agents.json"
  # The Claude engine needs a licence a human accepts; the seed must run without it.
  expect_out "the Small Seed runs on the open runners, never on the Claude engine" '^true$' \
    jq -e '[.items[] | select(.name == "founder" or .name == "router" or .name == "waker")] | length == 3 and
      all(.[]; .runner == "openai" or .runner == "echo")' "$WORK/agents.json"
  expect ok "the founder's home is in root's home, as a root agent's is" in_machine test -d /root/agents/founder
  expect ok "the router has its own Linux user and a home only it can open" small_seed_own_home router
  expect ok "the waker has its own Linux user and a home only it can open" small_seed_own_home waker
  skip "small-seed: founder runner" "always-on agents do not start at boot yet; the founder's runner starts when it is first woken"
}
