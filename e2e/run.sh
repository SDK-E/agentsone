#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# e2e: the Agents One scenario against the compose stack in compose.yaml.
#
# Usage:
#   e2e/run.sh up          start the stack (only the services whose code is built)
#   e2e/run.sh <stage>     run one stage
#   e2e/run.sh all         run every stage in order
#   e2e/run.sh list        list the stages
#   e2e/run.sh down        stop the stack and delete its volumes
#
# Each check prints PASS, FAIL or SKIP with its reason. A stage whose feature is not on main
# yet is SKIP, never PASS. The script exits 1 when any check fails.
#
# The stack comes from compose.yaml in the repository root (or COMPOSE_FILE), with
# e2e/compose.e2e.yaml on top: the fake model provider the scenario's model calls end at, built
# from e2e/fakeprovider and never part of a deployment. Images default to the release tag pinned
# in compose.yaml; set AGENTSONE_IMAGE_TAG to test another build, for example `main` (newest
# tested build of main) or `sha-<commit>` (CI does that).
set -uo pipefail

cd "$(dirname "$0")/.." || exit 2

# Every docker compose call below, and in the shells this script starts, sees the e2e overlay.
case ":${COMPOSE_FILE:=compose.yaml}:" in
  *:e2e/compose.e2e.yaml:*) ;;
  *) COMPOSE_FILE="${COMPOSE_FILE}:e2e/compose.e2e.yaml" ;;
esac
export COMPOSE_FILE

EDGE_IP="${AGENTSONE_EDGE_IP:-10.77.42.2}"
EDGE_OUTER_IP="${AGENTSONE_EDGE_OUTER_IP:-10.77.42.34}"
# Services `up` left out on purpose, one per line: "<service> <reason>".
SKIPPED="${AGENTSONE_E2E_STATE:-${RUNNER_TEMP:-${TMPDIR:-/tmp}}/agentsone-e2e-skipped}"
# What one run's stages hand on (the first human's sign-in, the session cookie). `up` empties it.
WORK="${AGENTSONE_E2E_DIR:-${RUNNER_TEMP:-${TMPDIR:-/tmp}}/agentsone-e2e}"
# The first human the setup page creates.
HUMAN="ada"
# The edge admin socket, as the control container mounts it.
ADMIN_DIR=/run/agentsone-edge-admin
# A throwaway client on the outer network, where control sits: what control could reach.
CURL_IMAGE="curlimages/curl:8.23.0@sha256:3fb7e13e5d179a845c97d78204f3f1064d1907037f26a3ebf91710571151fd93"

failures=0

pass() { printf 'PASS  %s\n' "$1"; }
fail() {
  printf 'FAIL  %s\n' "$1"
  [ -n "${2:-}" ] && printf '      %s\n' "$(printf '%s' "$2" | tr '\n' ' ' | cut -c1-200)"
  failures=$((failures + 1))
}
skip() {
  printf 'SKIP  %s: %s\n' "$1" "$2"
  if [ -n "${GITHUB_ACTIONS:-}" ]; then
    printf '::notice title=e2e stage skipped::%s: %s\n' "$1" "$2"
  fi
}

compose() { docker compose "$@"; }
in_machine() { compose exec -T machine "$@"; }
from_outer() {
  docker run --rm --network "$(compose config --format json | jq -r '.networks.outer.name')" "$CURL_IMAGE" "$@"
}

# expect <ok|fail> <label> <command...>
expect() {
  local want="$1" label="$2" out rc
  shift 2
  out="$("$@" 2>&1)"
  rc=$?
  if { [ "$want" = ok ] && [ "$rc" -eq 0 ]; } || { [ "$want" = fail ] && [ "$rc" -ne 0 ]; }; then
    pass "$label (exit $rc)"
  else
    fail "$label (exit $rc)" "$out"
  fi
}

# expect_out <label> <regex> <command...>: the command succeeds and prints a match.
expect_out() {
  local label="$1" regex="$2" out
  shift 2
  if out="$("$@" 2>&1)" && printf '%s' "$out" | grep -Eq "$regex"; then
    pass "$label"
  else
    fail "$label" "$out"
  fi
}

# expect_http <label> <status regex> <body regex> <curl command...>: the command answers with a
# matching status and, unless the body regex is empty, a matching body. The command must be a
# curl call; `-w` is added to read the status.
expect_http() {
  local label="$1" want_status="$2" want_body="$3" out status body
  shift 3
  out="$("$@" -w '\n%{http_code}' 2>&1)"
  status="${out##*$'\n'}"
  body="${out%$'\n'*}"
  if printf '%s' "$status" | grep -Eqx "$want_status" &&
    { [ -z "$want_body" ] || printf '%s' "$body" | grep -Eq -- "$want_body"; }; then
    pass "$label ($status)"
  else
    fail "$label (got $status)" "$body"
  fi
}

# door: set DOOR to the web door as a browser on this host opens it (the edge's published port).
door() {
  local hostport
  hostport="$(compose port edge 8080 2>/dev/null | head -n 1)"
  if [ -z "$hostport" ]; then
    fail "the web door has no published port"
    return 1
  fi
  DOOR="http://${hostport}"
}

# from_browser <method> <path> [curl args...]: a request to the web door as the interface sends
# it from a browser on the door's own origin, with this run's session cookie jar.
from_browser() {
  local method="$1" path="$2"
  shift 2
  curl -sS -m 10 -X "$method" -H "Origin: ${DOOR}" -H 'Content-Type: application/json' \
    -b "$WORK/cookies" -c "$WORK/cookies" "$@" "${DOOR}${path}"
}

# as_control <path> [curl args...]: call the edge admin socket holding only what the control
# container holds: the admin volume and the control user (65533 in the edge group 65532). No
# network, nothing else of the edge.
as_control() {
  local path="$1" volume
  shift
  volume="$(compose config --format json | jq -r '.volumes["edge-admin"].name')"
  docker run --rm --network none --user 65533:65532 -v "${volume}:${ADMIN_DIR}:ro" "$CURL_IMAGE" \
    -sS -m 10 --unix-socket "${ADMIN_DIR}/admin.sock" "$@" "http://edge${path}"
}

# signed_token: a gateway token signed inside the machine with its gateway key, as the daemon
# signs them (format ao1.<claims>.<HMAC-SHA256>). Root in the machine can always do this: the
# token identity is soft, and every hard wall in the edge holds whatever the token says.
signed_token() {
  # shellcheck disable=SC2016 # the script runs in the machine; it expands there.
  in_machine sh -c '
    set -e
    b64() { base64 -w0 | tr "+/" "-_" | tr -d "="; }
    now=$(date +%s)
    claims=$(printf "{\"agent\":\"system:e2e\",\"kind\":\"system\",\"chart\":\"\",\"defence\":false,\"aud\":\"gateway\",\"iat\":%s,\"exp\":%s}" "$now" "$((now + 300))" | b64)
    key=$(od -An -v -tx1 /run/agentsone/gateway-key/key | tr -d " \n")
    mac=$(printf "ao1.%s" "$claims" | openssl dgst -sha256 -mac HMAC -macopt "hexkey:$key" -binary | b64)
    printf "ao1.%s.%s" "$claims" "$mac"'
}

# needs <stage> <service...>: true when every service runs; otherwise SKIP (left out on purpose)
# or FAIL (it should run but does not).
needs() {
  local stage="$1" service reason
  shift
  for service in "$@"; do
    reason="$(awk -v s="$service" '$1 == s { $1 = ""; sub(/^ /, ""); print; exit }' "$SKIPPED" 2>/dev/null)"
    if [ -n "$reason" ]; then
      skip "$stage" "$reason"
      return 1
    fi
    if ! compose ps --status running --services 2>/dev/null | grep -qx "$service"; then
      fail "$stage: service $service is not running"
      return 1
    fi
  done
}

# not_on_main <stage> <work package> <what>: a scenario stage whose feature has not landed.
not_on_main() {
  skip "$1" "$3 is not on main yet (work package $2); the scenario is written when it lands"
}

image_of() { compose config --format json | jq -r --arg s "$1" '.services[$s].image'; }

left_out() { grep -q "^$1 " "$SKIPPED"; }

# start <service> <work package>: start one service and wait until it is healthy. A service whose
# program is still the placeholder ("not built yet", or a command the image does not have yet)
# is left out and recorded; any other failure to become healthy fails the run.
start() {
  local service="$1" wp="$2" logs
  if ! docker image inspect "$(image_of "$service")" >/dev/null 2>&1; then
    echo "$service the $service image is not built on main yet (work package $wp)" >>"$SKIPPED"
    return 0
  fi
  echo "starting $service"
  if compose up -d --no-deps --wait --wait-timeout 300 "$service"; then
    return 0
  fi
  logs="$(compose logs --no-color "$service" 2>&1)"
  if printf '%s' "$logs" | grep -Eq 'not built yet|unknown command'; then
    echo "$service the $service program is not built on main yet (work package $wp)" >>"$SKIPPED"
    compose rm -sf "$service" >/dev/null 2>&1
    return 0
  fi
  printf '%s\n' "$logs" | tail -n 40
  return 1
}

cmd_up() {
  : >"$SKIPPED"
  rm -rf "${WORK:?}" && mkdir -p "$WORK" && chmod 700 "$WORK" || return 1
  compose pull --quiet --ignore-pull-failures >/dev/null 2>&1 || true
  # Edge first: the machine's DNS and the control page depend on it. Without a built edge the
  # machine still starts, so the walls that do not need the edge (no route, no direct address,
  # no reach into the door) are tested anyway.
  start edge 15 || return 1
  start machine 14 || return 1
  start control 17 || return 1
  # The fake model provider is test code in this repository: it is always built, never left out.
  echo "building fakeprovider"
  compose build --quiet fakeprovider || return 1
  start fakeprovider e2e || return 1
  sed 's/^/left out: /' "$SKIPPED"
}

cmd_down() { compose down -v --remove-orphans; }

# --- network wall: real checks ------------------------------------------------------------

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

stage_wall_blocked() {
  needs wall-blocked machine || return 0
  expect fail "blocked name example.com does not resolve" in_machine getent hosts example.com
  expect fail "blocked: curl https://example.com" in_machine curl -sS -m 10 -o /dev/null https://example.com
  needs wall-blocked edge || return 0
  expect fail "blocked: forged SNI example.com to the edge :443" \
    in_machine curl -sS -m 10 -o /dev/null --resolve "example.com:443:${EDGE_IP}" https://example.com
  expect_out "blocked: Host example.com to the edge :80 is refused (403)" '^403$' \
    in_machine curl -sS -m 10 -o /dev/null -w '%{http_code}' -H 'Host: example.com' "http://${EDGE_IP}/"
}

stage_wall_allowed() {
  needs wall-allowed machine edge || return 0
  expect_out "allowed name deb.debian.org resolves to the edge" "^${EDGE_IP//./\\.} " \
    in_machine getent ahostsv4 deb.debian.org
  expect_out "allowed: https://github.com through the edge (TLS by name)" '^[23][0-9][0-9]$' \
    in_machine curl -sS -m 30 -o /dev/null -w '%{http_code}' https://github.com
  expect_out "allowed: http://deb.debian.org through the edge (HTTP by Host)" '^[23][0-9][0-9]$' \
    in_machine curl -sS -m 30 -o /dev/null -w '%{http_code}' http://deb.debian.org/debian/
}

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

# --- model gateway: the machine reaches it on the inner address, with a token only ----------

stage_gateway() {
  needs gateway machine edge || return 0
  local base="http://${EDGE_IP}:4000"
  expect_out "machine reaches the model gateway; no token is refused (401)" '^401$' \
    in_machine curl -sS -m 5 -o /dev/null -w '%{http_code}' "${base}/v1/models"
  expect_out "a forged token is refused (401)" '^401$' \
    in_machine curl -sS -m 5 -o /dev/null -w '%{http_code}' -X POST -H 'Authorization: Bearer forged' \
    -H 'Content-Type: application/json' -d '{"model":"autoagents/one"}' "${base}/v1/chat/completions"
  expect_out "the gateway serves no admin route to the machine (404)" '^404$' \
    in_machine curl -sS -m 5 -o /dev/null -w '%{http_code}' "${base}/v1/gateway/budget"
  expect_out "the machine cannot change the budget through the gateway (404 or 405)" '^40[45]$' \
    in_machine curl -sS -m 5 -o /dev/null -w '%{http_code}' -X PATCH -d '{}' "${base}/v1/budget"
}

# --- audit sink: only the daemon may send machine records -------------------------------------

stage_audit_sink() {
  needs audit-sink machine edge || return 0
  local key=/run/agentsone/gateway-key/key sink="http://${EDGE_IP}:7710/v1/audit/records"
  # An agent user's id (agents hold 200000 to 299999). Checks only whether the key is readable;
  # nothing here prints it.
  local as_agent=(setpriv --reuid=299999 --regid=299999 --clear-groups)
  expect ok "the gateway key is mounted in the machine" in_machine test -f "$key"
  expect ok "a command can run as an agent's user" in_machine "${as_agent[@]}" true
  expect fail "an agent's user cannot read the gateway key" in_machine "${as_agent[@]}" test -r "$key"
  expect_out "a record with no token is refused (401)" '^401$' \
    in_machine "${as_agent[@]}" curl -sS -m 5 -o /dev/null -w '%{http_code}' -X POST \
    -H 'Content-Type: application/json' -d '{"records":[{}]}' "$sink"
  expect_out "a record with a forged token is refused (401)" '^401$' \
    in_machine "${as_agent[@]}" curl -sS -m 5 -o /dev/null -w '%{http_code}' -X POST \
    -H 'Authorization: Bearer forged' -H 'Content-Type: application/json' -d '{"records":[{}]}' "$sink"
}

# --- rescue: a restart keeps the container layer, a recreate resets it to the image ----------

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

# --- web door: humans reach the machine through the edge, and a header is never an identity --

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

# --- setup page and login: the first human, only through the web door ----------------------

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

# --- scenario stages ----------------------------------------------------------------------

stage_small_seed() { not_on_main small-seed 18 "the Small Seed plugin"; }
stage_hire() { not_on_main hire 1 "hiring agents"; }
stage_bus() { not_on_main bus 3 "the bus"; }
stage_router() { not_on_main router 4 "the live list and router"; }
stage_echo_chat() { not_on_main echo-chat 9 "chat with the echo runner"; }
stage_event_to_task() { not_on_main event-to-task 3 "events and triggers"; }
stage_memory() { not_on_main memory 5 "memory"; }
stage_plugin_install() { not_on_main plugin-install 6 "plugin install"; }
stage_agentctl_parity() { not_on_main agentctl-parity 8 "agentctl against the running machine"; }

# --- freeze all: a hard wall in the edge, switched only through the admin socket --------------

# model_call <token> [curl args...]: one chat completion from the machine to the gateway, as a
# runner sends it.
model_call() {
  local token="$1"
  shift
  in_machine curl -sS -m 10 -X POST -H "Authorization: Bearer ${token}" -H 'Content-Type: application/json' \
    -d '{"model":"autoagents/one","messages":[{"role":"user","content":"e2e"}]}' "$@" \
    "http://${EDGE_IP}:4000/v1/chat/completions"
}

stage_freeze_all() {
  needs freeze-all machine edge || return 0
  local token
  if ! token="$(signed_token)" || [ -z "$token" ]; then
    fail "freeze-all: sign a gateway token in the machine" "$token"
    return 0
  fi
  expect_http "freeze all is off at the start" '200' '"frozen":false' as_control /v1/freeze
  # No provider is set up here, so an unfrozen call ends at routing (503), past the freeze gate.
  expect_http "before the freeze a signed model call passes the freeze gate" '(200|402|502|503)' '' model_call "$token"
  expect_http "a human freezes all through the admin socket" '200' '"frozen":true' as_control /v1/freeze -X POST
  expect_http "frozen: a model call is refused" '423' 'urn:agentsone:problem:frozen' model_call "$token"
  expect fail "frozen: the machine cannot reach an allowed site (https://github.com)" \
    in_machine curl -sS -m 10 -o /dev/null https://github.com
  expect ok "restart the edge" \
    sh -c 'docker compose restart edge && docker compose up -d --no-deps --wait --wait-timeout 300 edge'
  expect_http "after an edge restart freeze all is still on" '200' '"frozen":true' as_control /v1/freeze
  expect_http "after an edge restart a model call is still refused" '423' 'urn:agentsone:problem:frozen' model_call "$token"
  expect fail "after an edge restart the machine still cannot reach https://github.com" \
    in_machine curl -sS -m 10 -o /dev/null https://github.com
  expect_http "a human releases the freeze" '200' '"frozen":false' as_control /v1/freeze -X DELETE
  expect_http "released: a model call passes the freeze gate again" '(200|402|502|503)' '' model_call "$token"
  expect_out "released: the machine reaches https://github.com again" '^[23][0-9][0-9]$' \
    in_machine curl -sS -m 30 -o /dev/null -w '%{http_code}' https://github.com
  expect_http "the edge proof log records the freeze and the release" '200' '"freeze\.on".*"freeze\.off"' \
    as_control '/v1/audit/proof?limit=1000'
}

# --- control page: the human-only page on its own port, set up with the code from its log ----

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

# --- model turn: a real model call through the gateway, ending at the e2e fake provider --------

# The provider e2e/fakeprovider/gateway.toml defines.
FAKE_PROVIDER="e2e-fake"
# What the fake answers (e2e/fakeprovider/main.go): the reason it gives its pick, its chat reply,
# and what each answer costs at the e2e prices, in micro-dollars.
FAKE_ROUTE_REASON="Picked by the e2e fake provider."
FAKE_CHAT_REPLY="Hello from the e2e fake provider."
FAKE_COST=1100
# The request a human routes; the fake rates whatever the router shortlists.
ROUTE_TEXT="Please write the release notes for this week."

# as_control_in <path> [curl args...]: as as_control, with this script's stdin as the JSON
# request body, so a secret is never on a command line.
as_control_in() {
  local path="$1" volume
  shift
  volume="$(compose config --format json | jq -r '.volumes["edge-admin"].name')"
  docker run --rm -i --network none --user 65533:65532 -v "${volume}:${ADMIN_DIR}:ro" "$CURL_IMAGE" \
    -sS -m 10 --unix-socket "${ADMIN_DIR}/admin.sock" -H 'Content-Type: application/json' --data-binary @- \
    "$@" "http://edge${path}"
}

# seed_gateway_config: write e2e/fakeprovider/gateway.toml into the edge data volume as the edge
# user, as a human edits the file on the host. It names the provider and its address only: the
# provider is switched on and its key set through the admin socket.
seed_gateway_config() {
  local volume dir=/var/lib/agentsone-edge
  volume="$(compose config --format json | jq -r '.volumes["edge-data"].name')"
  docker run --rm -i --network none --user 65532:65532 --cap-drop ALL --security-opt no-new-privileges \
    --read-only -v "${volume}:${dir}" --entrypoint sh "$CURL_IMAGE" \
    -c "cat >${dir}/.gateway-e2e.toml && chmod 600 ${dir}/.gateway-e2e.toml && mv -f ${dir}/.gateway-e2e.toml ${dir}/gateway.toml" \
    <e2e/fakeprovider/gateway.toml
}

# The fake provider's notes of the calls it got (no keys, only their hashes).
FAKE_SEEN_URL="http://fakeprovider:8080/seen"

# fake_seen: the fake provider's notes, asked from the outer network.
fake_seen() { from_outer -sS -m 10 "$FAKE_SEEN_URL"; }

# fake_calls: how many calls reached the fake provider.
fake_calls() { fake_seen | jq -e '.calls | length'; }

# fake_saw_only_the_key <from> [kind]: the calls after the first <from> reached the fake provider,
# at least one of them (of that kind), each with this run's provider key and none with a gateway
# token in a header or the body.
fake_saw_only_the_key() {
  fake_seen | jq -e --argjson from "$1" --arg kind "${2:-}" --arg hash "$(provider_auth_hash)" '
    .calls[$from:] as $new
    | ($new | length) > 0
      and all($new[]; .auth_sha256 == $hash and (.gateway_token | not))
      and ($kind == "" or any($new[]; .kind == $kind))'
}

# provider_auth_hash: the SHA-256 of the Authorization header the fake should see. Not secret.
provider_auth_hash() { printf 'Bearer %s' "$(cat "$WORK/provider-key")" | openssl dgst -sha256 -r | cut -c1-64; }

# budget_field <field>: one number from the edge budget, through the admin socket.
budget_field() { as_control /v1/gateway/budget | jq -e --arg f "$1" '.[$f] | numbers'; }

# set_budget_total <micro-dollars> [curl args...]: a human sets the total budget through the admin
# socket.
set_budget_total() {
  local total="$1"
  shift
  printf '{"total_micros":%s}' "$total" | as_control_in /v1/gateway/budget -X PATCH "$@"
}

# spent_grew <before> <at least>: the budget spent is now at least <at least> micro-dollars above
# <before>.
spent_grew() {
  local now
  now="$(budget_field spent_micros)" || return 1
  echo "spent ${1} then ${now}"
  [ $((now - $1)) -ge "$2" ]
}

# provider_listed <fingerprint>: the admin provider list has one provider on, the fake, with a key
# of that fingerprint.
provider_listed() {
  as_control /v1/gateway/providers | jq -e --arg id "$FAKE_PROVIDER" --arg fp "$1" \
    '[.providers[] | select(.enabled)] | length == 1 and .[0].id == $id and .[0].key.fingerprint == $fp'
}

# proof_calls <agent>: the edge proof records of model calls by <agent>, oldest first, one per line.
proof_calls() {
  local after=0 page n
  while :; do
    page="$(as_control "/v1/audit/proof?after=${after}&limit=500")" || return 1
    printf '%s' "$page" | jq -c --arg a "$1" '.records[] | select(.action == "model.call" and .target.id == $a)' ||
      return 1
    n="$(printf '%s' "$page" | jq -e '.records | length')" || return 1
    [ "$n" -lt 500 ] && return 0
    after="$(printf '%s' "$page" | jq -e '.records[-1].seq')" || return 1
  done
}

# proof_count <agent>: how many model call proof records <agent> has.
proof_count() {
  local lines
  lines="$(proof_calls "$1")" || return 1
  printf '%s' "$lines" | grep -c '^{' || true
}

# expect_proof <label> <agent> <count before> <jq test>: <agent> has one more model call proof
# record than before, and the newest passes the test.
expect_proof() {
  local label="$1" agent="$2" before="$3" test="$4" lines newest
  if ! lines="$(proof_calls "$agent")"; then
    fail "$label" "the edge proof log is not readable"
    return
  fi
  newest="$(printf '%s\n' "$lines" | grep '^{' | tail -n 1)"
  if [ "$(printf '%s\n' "$lines" | grep -c '^{')" -eq "$((before + 1))" ] &&
    printf '%s' "$newest" | jq -e "$test" >/dev/null 2>&1; then
    pass "$label"
  else
    fail "$label" "${newest:-no model call record for $agent}"
  fi
}

# key_never_shown: the admin socket's provider list does not hold the provider key. Prints nothing
# that could hold it.
key_never_shown() {
  local list
  list="$(as_control /v1/gateway/providers)" || return 1
  ! printf '%s' "$list" | grep -qF -- "$(cat "$WORK/provider-key")"
}

stage_model_turn() {
  needs model-turn machine edge fakeprovider || return 0
  local fake_ip outer fp token calls spent proofs

  # The fake provider is outside the wall: the edge reaches it, the machine cannot.
  outer="$(compose config --format json | jq -r '.networks.outer.name')"
  fake_ip="$(docker inspect -f "{{(index .NetworkSettings.Networks \"${outer}\").IPAddress}}" "$(compose ps -q fakeprovider)" 2>/dev/null)"
  expect fail "the machine cannot resolve the fake provider" in_machine getent hosts fakeprovider
  if [ -n "$fake_ip" ]; then
    expect fail "the machine cannot reach the fake provider by address (${fake_ip}:8080)" \
      in_machine curl -sS -m 5 -o /dev/null "http://${fake_ip}:8080/healthz"
  else
    fail "model-turn: the fake provider's address is not found"
  fi
  expect_http "the outer network reaches the fake provider" '200' '"calls":' from_outer -sS -m 10 "$FAKE_SEEN_URL"

  # A human sets the provider up: its address in gateway.toml, the rest through the admin socket.
  (umask 077 && openssl rand -hex 24 >"$WORK/provider-key") || {
    fail "model-turn: make a provider key"
    return 0
  }
  # The edge fingerprint is the first 8 hex digits of the key's SHA-256 (adminapi.Fingerprint).
  fp="$(printf '%s' "$(cat "$WORK/provider-key")" | openssl dgst -sha256 -r | cut -c1-8)"
  expect ok "gateway.toml names the fake provider (written as the edge user, as a human edits it)" seed_gateway_config
  expect_http "a human switches the fake provider on through the admin socket" '200' \
    "\"id\":\"${FAKE_PROVIDER}\",\"enabled\":true,\"key_required\":true,\"key\":\\{\"set\":false\\}" \
    as_control "/v1/gateway/providers/${FAKE_PROVIDER}" -X PUT
  expect_http "a human sets a key made for this run; the edge answers with its fingerprint only" '200' \
    "^\\{\"set\":true,\"fingerprint\":\"${fp}\"\\}\$" \
    as_control_in "/v1/gateway/providers/${FAKE_PROVIDER}/key" -X PUT < <(jq -Rc '{key: .}' "$WORK/provider-key")
  expect_out "the provider list shows only the fake provider on, with the key's fingerprint" '^true$' \
    provider_listed "$fp"
  expect ok "the provider list never shows the key" key_never_shown
  expect_http "a human sets a total budget of 1 USD through the admin socket" '200' '"total_micros":1000000' \
    set_budget_total 1000000

  # The gateway leg: a call signed like the daemon's tokens, from the machine.
  if ! token="$(signed_token)" || [ -z "$token" ]; then
    fail "model-turn: sign a gateway token in the machine" "$token"
    return 0
  fi
  if ! { calls="$(fake_calls)" && spent="$(budget_field spent_micros)" && proofs="$(proof_count system:e2e)"; }; then
    fail "model-turn: read the fake provider, the budget and the proof log before the call"
    return 0
  fi
  expect_http "a model call from the machine is answered by the fake provider through the gateway" '200' \
    "\"content\":\"${FAKE_CHAT_REPLY}\"" model_call "$token"
  expect ok "the fake provider got the provider key, never the gateway token" fake_saw_only_the_key "$calls" chat
  expect_proof "the edge proof log has the call: system caller, the fake's model, its tokens and cost" \
    system:e2e "$proofs" \
    ".outcome == \"success\" and .details.kind == \"system\" and .details.status == 200 and
     (.details.model | startswith(\"${FAKE_PROVIDER}/\")) and .details.input_tokens == 1000 and
     .details.output_tokens == 50 and .details.cost_micros == ${FAKE_COST}"
  expect ok "the budget spent grew by at least the call's cost (${FAKE_COST} micro-dollars)" \
    spent_grew "$spent" "$FAKE_COST"

  # The router leg: a human request through the web door, rated by the router's own model call
  # with the token the daemon mints for system:router.
  door || return 0
  local seeded
  seeded="$(curl -sS -m 10 "${DOOR}/v1/setup" | jq -r '.seed_installed')"
  if [ "$seeded" != true ]; then
    skip "model-turn: router leg" "no router agent: GET /v1/setup says seed_installed ${seeded:-unreadable}; the daemon does not wire the runner supervisor yet, so the Small Seed never installs"
    return 0
  fi
  if [ ! -s "$WORK/login.json" ]; then
    fail "model-turn: no first human; run the setup-page stage first"
    return 0
  fi
  if ! { calls="$(fake_calls)" && spent="$(budget_field spent_micros)" && proofs="$(proof_count system:router)"; }; then
    fail "model-turn: read the fake provider, the budget and the proof log before routing"
    return 0
  fi
  rm -f "$WORK/cookies" "$WORK/assignment.json"
  expect_http "the first human signs in through the web door" '201' "\"name\":\"${HUMAN}\"" \
    from_browser POST /v1/session -d @"$WORK/login.json"
  expect_http "a human request through the web door is routed to an agent's chat" '201' '' \
    from_browser POST /v1/router/route -d "$(jq -nc --arg t "$ROUTE_TEXT" '{text: $t}')" -o "$WORK/assignment.json"
  # shellcheck disable=SC2016 # $r is a jq variable.
  expect_out "the router's model call picked the agent: its pick carries the fake provider's reason" '^true$' \
    jq -e --arg r "$FAKE_ROUTE_REASON" '.chosen == .shortlist[0].agent and .shortlist[0].reason == $r' "$WORK/assignment.json"
  local chat_path message_id
  chat_path="$(jq -r '.chat_path // empty' "$WORK/assignment.json" 2>/dev/null)"
  message_id="$(jq -r '.chat_message_id // empty' "$WORK/assignment.json" 2>/dev/null)"
  if [ -n "$chat_path" ] && [ -n "$message_id" ]; then
    expect_http "the request is in the chosen agent's chat" '200' "\"id\":\"${message_id}\"" from_browser GET "$chat_path"
    expect_http "the chat message holds the human's text" '200' "\"text\":\"${ROUTE_TEXT}\"" from_browser GET "$chat_path"
  else
    fail "model-turn: the assignment names no chat message" "$(cat "$WORK/assignment.json" 2>/dev/null)"
  fi
  expect ok "the router's call reached the fake provider with the provider key, never the router's token" \
    fake_saw_only_the_key "$calls" route
  expect_proof "the edge proof log has the router's call: agent system:router, system kind, status 200" \
    system:router "$proofs" \
    ".outcome == \"success\" and .details.kind == \"system\" and .details.status == 200 and
     .details.model == \"${FAKE_PROVIDER}/fake-quick\" and .details.cost_micros == ${FAKE_COST}"
  expect ok "the budget spent grew by at least the router's call (${FAKE_COST} micro-dollars)" \
    spent_grew "$spent" "$FAKE_COST"
}

# --- budget wall: the total is a hard wall in the edge; past it nothing reaches a provider ------

stage_budget_wall() {
  needs budget-wall machine edge fakeprovider || return 0
  if [ ! -s "$WORK/provider-key" ]; then
    fail "budget-wall: no provider is set up; run the model-turn stage first"
    return 0
  fi
  local token total spent calls proofs
  if ! token="$(signed_token)" || [ -z "$token" ]; then
    fail "budget-wall: sign a gateway token in the machine" "$token"
    return 0
  fi
  if ! { total="$(budget_field total_micros)" && spent="$(budget_field spent_micros)" && proofs="$(proof_count system:e2e)"; }; then
    fail "budget-wall: read the budget and the proof log"
    return 0
  fi
  expect_http "a human sets the total to what is already spent" '200' '"exhausted":true' set_budget_total "$spent"
  calls="$(fake_calls)" || {
    fail "budget-wall: read the fake provider"
    return 0
  }
  expect_http "budget used up: a model call is refused" '402' 'urn:agentsone:problem:budget-exhausted' model_call "$token"
  expect_out "budget used up: nothing reached the fake provider" "^${calls}\$" fake_calls
  expect_proof "the edge proof log has the refused call as denied (402)" system:e2e "$proofs" \
    '.outcome == "denied" and .details.status == 402 and .details.cost_micros == 0'
  expect_http "a human puts the total back" '200' '"exhausted":false' set_budget_total "$total"
  expect_http "budget back: a model call is answered by the fake provider again" '200' \
    "\"content\":\"${FAKE_CHAT_REPLY}\"" model_call "$token"
  expect ok "budget back: the call reached the fake provider with the provider key only" fake_saw_only_the_key "$calls" chat
}

STAGES=(
  wall-route wall-direct wall-blocked wall-allowed wall-outer gateway audit-sink rescue web-door
  setup-page login small-seed hire bus router model-turn echo-chat event-to-task memory
  plugin-install agentctl-parity budget-wall freeze-all control
)

run_stage() {
  local fn="stage_${1//-/_}"
  if ! declare -F "$fn" >/dev/null; then
    echo "unknown stage: $1 (see: e2e/run.sh list)" >&2
    exit 2
  fi
  echo "== $1"
  "$fn"
}

case "${1:-}" in
  up) cmd_up || exit 1 ;;
  down) cmd_down ;;
  list) printf '%s\n' "${STAGES[@]}" ;;
  all) for s in "${STAGES[@]}"; do run_stage "$s"; done ;;
  '' | -h | --help) sed -n '4,20p' "$0" | sed 's/^# \{0,1\}//' ;;
  *) for s in "$@"; do run_stage "$s"; done ;;
esac

if [ "$failures" -gt 0 ]; then
  echo "FAILURES: $failures"
  exit 1
fi
