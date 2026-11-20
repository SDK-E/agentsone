# SPDX-License-Identifier: MIT
# shellcheck shell=bash
#
# Login: the first human signs in and out, only through the web door.

stage login "the first human signs in and out through the web door"

stage_login() {
  needs login machine edge || return 0
  door || return 0
  if [ ! -s "$WORK/login.json" ]; then
    fail "login: no first human; run the setup-page stage first"
    return 0
  fi
  jq -c '.password = "wrong-password-for-e2e"' "$WORK/login.json" >"$WORK/wrong.json"
  rm -f "$WORK/cookies"
  expect_http "a wrong password is refused" '401' '' from_browser POST /v1/session -d @"$WORK/wrong.json"
  expect_http "the first human signs in through the web door" '201' "\"name\":\"${HUMAN}\"" \
    from_browser POST /v1/session -d @"$WORK/login.json" -D "$WORK/login.headers"
  expect_out "the session cookie is HttpOnly and SameSite=Strict" 'HttpOnly' \
    grep -i '^set-cookie: agentsone_session=.*samesite=strict' "$WORK/login.headers"
  expect_http "the session names the human" '200' "\"name\":\"${HUMAN}\"" from_browser GET /v1/session/me
  expect_http "without the cookie nobody is signed in" '401' 'problem:unauthenticated' \
    curl -sS -m 10 "${DOOR}/v1/session/me"
  local session
  session="$(awk '$6 == "agentsone_session" { print $7 }' "$WORK/cookies" 2>/dev/null)"
  expect_http "signing out ends the session" '204' '' from_browser DELETE /v1/session
  expect_http "after signing out the old cookie no longer works" '401' '' \
    curl -sS -m 10 -b "agentsone_session=${session:-none}" "${DOOR}/v1/session/me"
}
