# SPDX-License-Identifier: MIT
# shellcheck shell=bash
#
# Setup page: the first human, only through the web door. It leaves the first human's sign-in in
# $WORK/login.json for the stages after it.

stage setup-page "the first human is made on the setup page, only through the web door"

stage_setup_page() {
  needs setup-page machine edge || return 0
  door || return 0
  local password
  password="$(openssl rand -hex 16)" || { fail "setup-page: make a password"; return 0; }
  (
    umask 077
    jq -nc --arg n "$HUMAN" --arg p "$password" '{name: $n, display_name: "Ada (e2e)", password: $p}' >"$WORK/first-human.json"
    jq -nc --arg n "$HUMAN" --arg p "$password" '{name: $n, password: $p}' >"$WORK/login.json"
    jq -nc --arg n "$HUMAN" '{name: $n, display_name: "Ada (e2e)", password: "short"}' >"$WORK/weak.json"
  )
  local agent_try='{"name":"mallory","display_name":"Mallory","password":"agents-are-not-humans"}'
  expect_http "before setup the machine asks for its first human" '200' '"phase":"needs-human"' \
    curl -sS -m 10 "${DOOR}/v1/setup"
  expect_http "the machine cannot create the first human on its loopback address" '403' 'Open the web door to finish setup' \
    in_machine curl -sS -m 10 -X POST -H 'Content-Type: application/json' -d "$agent_try" \
    http://127.0.0.1:7700/v1/setup/first-human
  expect_http "the machine cannot create the first human on the daemon socket" '403' 'Open the web door to finish setup' \
    in_machine curl -sS -m 10 --unix-socket /run/agentsone/api.sock -X POST -H 'Content-Type: application/json' \
    -d "$agent_try" http://agentsoned/v1/setup/first-human
  expect_http "the setup page refuses a password under 12 characters" '400' 'password must be 12 to 256 characters' \
    from_browser POST /v1/setup/first-human -d @"$WORK/weak.json"
  expect_http "the setup page creates the first human through the web door" '201' "\"name\":\"${HUMAN}\"" \
    from_browser POST /v1/setup/first-human -d @"$WORK/first-human.json"
  expect_http "setup now says the first human exists" '200' '"first_human_created":true' \
    curl -sS -m 10 "${DOOR}/v1/setup"
  expect_http "a second first human is refused" '409' 'problem:conflict' \
    from_browser POST /v1/setup/first-human -d @"$WORK/first-human.json"
}
