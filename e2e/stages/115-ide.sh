# SPDX-License-Identifier: MIT
# shellcheck shell=bash
#
# IDE: the first human opens their own editor (the VSCodium server) through the web door, framed
# only by the interface, closed to anyone not signed in, and installs an extension from Open VSX
# through the edge allow list.

stage ide "the first human opens their own editor through the web door and installs an extension from Open VSX"

# ide_running: the first human's editor server says it runs.
ide_running() {
  local out
  out="$(from_browser GET "/v1/ide/servers/${HUMAN}")" || return 1
  printf '%s\n' "$out"
  printf '%s' "$out" | jq -e '.state == "running"' >/dev/null
}

# ide_cli <args...>: the editor's own command line, as the first human's Linux user.
ide_cli() {
  compose exec -T -u "h_${HUMAN}" -e "HOME=/home/humans/${HUMAN}" machine \
    /opt/vscodium-server/bin/codium-server "$@"
}

stage_ide() {
  needs ide machine edge || return 0
  door || return 0
  if [ ! -s "$WORK/login.json" ]; then
    fail "ide: no first human; run the setup-page stage first"
    return 0
  fi
  # The login stage signs out at its end.
  rm -f "$WORK/cookies"
  expect_http "the first human signs in again" '201' '' from_browser POST /v1/session -d @"$WORK/login.json"
  expect_http "the IDE status names the open build" '200' '"build":"vscodium"' from_browser GET /v1/ide
  expect_http "the human starts their own editor" '202' '"state":"(starting|running)"' \
    from_browser PUT "/v1/ide/servers/${HUMAN}"
  expect ok "the editor runs within two minutes" wait_for 120 ide_running
  expect_http "the editor opens through the web door" '200' 'workbench' \
    from_browser GET "/ide/${HUMAN}/" -D "$WORK/ide.headers"
  expect_out "only the interface may frame the editor" 'SAMEORIGIN' grep -i '^x-frame-options:' "$WORK/ide.headers"
  expect_out "the editor's policy allows same-origin frames" "frame-ancestors 'self'" \
    grep -i '^content-security-policy:' "$WORK/ide.headers"
  expect_http "without the session the editor is closed" '401' '' curl -sS -m 10 "${DOOR}/ide/${HUMAN}/"
  expect ok "an extension installs from Open VSX through the edge" ide_cli --install-extension EditorConfig.EditorConfig
  expect_out "the extension is installed for the human" '[Ee]ditor[Cc]onfig\.[Ee]ditor[Cc]onfig' ide_cli --list-extensions
  expect_http "the human stops their editor" '204' '' from_browser DELETE "/v1/ide/servers/${HUMAN}"
  expect_http "a stopped editor does not open" '503' '' from_browser GET "/ide/${HUMAN}/"
  expect_http "the human signs out" '204' '' from_browser DELETE /v1/session
}
