# SPDX-License-Identifier: MIT
# shellcheck shell=bash
#
# Control page: the human-only page on its own port, set up with the code from its log.

stage control "the control page works on its own port with its own passphrase"

# control_page <method> <path> [curl args...]: a request to the control page as a browser on its
# own origin sends it, with this run's control cookie jar.
control_page() {
  local method="$1" path="$2"
  shift 2
  curl -sS -m 10 -X "$method" -H "Origin: ${CONTROL}" -b "$WORK/control-cookies" -c "$WORK/control-cookies" \
    "$@" "${CONTROL}${path}"
}

# control_csrf <path>: the form token on a control page.
control_csrf() {
  control_page GET "$1" | grep -o 'name="csrf" value="[^"]*"' | head -n 1 | cut -d '"' -f 4
}

stage_control() {
  needs control control edge || return 0
  local hostport code csrf passphrase
  hostport="$(compose port control 8081 2>/dev/null | head -n 1)"
  if [ -z "$hostport" ]; then
    fail "control: the control page has no published port"
    return 0
  fi
  CONTROL="http://${hostport}"
  rm -f "$WORK/control-cookies"
  passphrase="$(openssl rand -hex 16)" || { fail "control: make a passphrase"; return 0; }
  expect_http "the control page answers on its own port and asks for setup" '200' 'name="code"' \
    control_page GET /setup
  csrf="$(control_csrf /setup)"
  # Read after the first visit: if the edge was still starting, that visit prints the code.
  code="$(compose logs --no-color control 2>/dev/null | grep -Eo 'setup_code[=":]+[A-Za-z0-9-]+' | tail -n 1 |
    sed -E 's/^setup_code[=":]+//')"
  expect_http "a wrong setup code is refused" '400' 'This is not the setup code' \
    control_page POST /setup --data-urlencode "csrf=${csrf}" --data-urlencode 'code=not-the-code' \
    --data-urlencode "passphrase=${passphrase}" --data-urlencode "confirm=${passphrase}"
  if [ -z "$code" ]; then
    fail "control: the setup code is in the control log" "$(compose logs --no-color control 2>&1 | tail -n 5)"
    return 0
  fi
  expect_http "the setup code from the control log sets the passphrase and signs in" '303' '' \
    control_page POST /setup --data-urlencode "csrf=${csrf}" --data-urlencode "code=${code}" \
    --data-urlencode "passphrase=${passphrase}" --data-urlencode "confirm=${passphrase}"
  expect_http "signed in, the control page shows freeze all" '200' 'action="/freeze"' control_page GET /
  csrf="$(control_csrf /)"
  expect_http "a form from another site is refused" '403' 'This form has expired' \
    curl -sS -m 10 -X POST -H 'Origin: https://evil.example' -b "$WORK/control-cookies" \
    --data-urlencode "csrf=${csrf}" "${CONTROL}/freeze"
  expect_http "a form without this session's token is refused" '403' 'This form has expired' \
    control_page POST /freeze --data-urlencode 'csrf=not-the-token'
  expect_http "freeze all from the control page" '303' '' control_page POST /freeze --data-urlencode "csrf=${csrf}"
  expect_http "the edge is frozen" '200' '"frozen":true' as_control /v1/freeze
  expect_http "release from the control page" '303' '' control_page POST /release --data-urlencode "csrf=${csrf}"
  expect_http "the edge is released" '200' '"frozen":false' as_control /v1/freeze
  expect_http "sign out of the control page" '303' '' control_page POST /sign-out --data-urlencode "csrf=${csrf}"
  csrf="$(control_csrf /sign-in)"
  expect_http "a wrong passphrase is refused" '401' '' \
    control_page POST /sign-in --data-urlencode "csrf=${csrf}" --data-urlencode 'passphrase=wrong-passphrase-e2e'
  expect_http "the passphrase signs in again" '303' '' \
    control_page POST /sign-in --data-urlencode "csrf=${csrf}" --data-urlencode "passphrase=${passphrase}"
  expect_http "signed in again, the control page shows freeze all" '200' 'action="/freeze"' control_page GET /
}
