# SPDX-License-Identifier: MIT
# shellcheck shell=bash
#
# Web door: humans reach the machine through the edge, and a header is never an identity.

stage web-door "humans reach the daemon through the web door; a header is never an identity"

stage_web_door() {
  needs web-door machine edge || return 0
  door || return 0
  expect_http "the web door on this host reaches the machine's daemon" '200' '"contract_version"' \
    curl -sS -m 10 "${DOOR}/v1/setup"
  expect_http "the web door serves the interface" '200' '^<!doctype html>' curl -sS -m 10 "${DOOR}/"
  expect_http "identity headers sent to the web door sign nobody in" '401' 'problem:unauthenticated' \
    curl -sS -m 10 -H 'Remote-User: ada' -H 'X-Forwarded-User: ada' -H 'X-Auth-Request-User: ada' \
    "${DOOR}/v1/session/me"
  expect_http "a page on another site cannot post through the web door" '403' 'cross-origin request refused' \
    curl -sS -m 10 -X POST -H 'Origin: https://evil.example' -H 'Content-Type: application/json' \
    -d '{"name":"ada","password":"not-the-password"}' "${DOOR}/v1/session"
  expect_http "a forged X-Forwarded-Host does not make another site same-origin" '403' 'cross-origin request refused' \
    curl -sS -m 10 -X POST -H 'Origin: https://evil.example' -H 'X-Forwarded-Host: evil.example' \
    -H 'Content-Type: application/json' -d '{"name":"ada","password":"not-the-password"}' "${DOOR}/v1/session"
}
