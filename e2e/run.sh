#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# e2e: the Agents One scenario against the compose stack in compose.yaml.
#
# Usage:
#   e2e/run.sh up          start the stack (only the services whose code is built)
#   e2e/run.sh <stage>     run one stage
#   e2e/run.sh all         run every stage in order
#   e2e/run.sh list        list the stages and what they cover of the plan's Verification list
#   e2e/run.sh down        stop the stack and delete its volumes
#
# Each check prints PASS, FAIL or SKIP with its reason. A stage that is not a real check yet is
# SKIP with the reason, never PASS. The script exits 1 when any check fails.
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

# Some settings are read only by the stage files this script sources, hence the SC2034 notes.
# shellcheck disable=SC2034 # read by the stage files
EDGE_IP="${AGENTSONE_EDGE_IP:-10.77.42.2}"
# shellcheck disable=SC2034 # read by the stage files
EDGE_OUTER_IP="${AGENTSONE_EDGE_OUTER_IP:-10.77.42.34}"
# Services `up` left out on purpose, one per line: "<service> <reason>".
SKIPPED="${AGENTSONE_E2E_STATE:-${RUNNER_TEMP:-${TMPDIR:-/tmp}}/agentsone-e2e-skipped}"
# What one run's stages hand on (the first human's sign-in, the session cookie). `up` empties it.
WORK="${AGENTSONE_E2E_DIR:-${RUNNER_TEMP:-${TMPDIR:-/tmp}}/agentsone-e2e}"
# The first human the setup page creates.
# shellcheck disable=SC2034 # read by the stage files
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

# --- stages ------------------------------------------------------------------------------------
#
# Each stage is one file, e2e/stages/<NNN>-<name>.sh, and the stages run in the order of their
# numbers. This script sources every file in e2e/lib/ (helpers several stages share), then every
# stage file. Sourcing a file only defines things; it runs nothing. A stage file holds:
#
#   stage <name> "<what it checks>"   once: <name> is the file's name after the number
#   stage_<name>() { ... }            the checks, with the dashes in <name> as underscores
#   pending <name> "<why>"            instead of the checks, while the stage is not a real check
#
# A helper only one stage uses lives in that stage's file, named after the stage. The e2e job in
# .github/workflows/images.yml runs each stage as its own step. `e2e/run.sh list` checks all of
# this, and fails when any of it breaks.

STAGES=()   # "<name>|<what it checks>", in run order
PENDING=()  # "<name>|<why it is not a real check yet>"
PROBLEMS=() # what is wrong with the stage files, for `list`
STAGE_FILE=""

# file_stage <file>: the stage name in a stage file's name.
file_stage() {
  local base="${1##*/}"
  base="${base%.sh}"
  printf '%s' "${base#*-}"
}

# stage <name> <what it checks>: register the stage of the file being loaded.
stage() {
  [ "$1" = "$(file_stage "$STAGE_FILE")" ] ||
    PROBLEMS+=("$STAGE_FILE registers stage $1, but its file name says $(file_stage "$STAGE_FILE")")
  STAGES+=("$1|$2")
}

# pending <name> <why>: the stage is not a real check yet; running it is SKIP with this reason.
pending() {
  [ "$1" = "$(file_stage "$STAGE_FILE")" ] ||
    PROBLEMS+=("$STAGE_FILE marks stage $1 pending, but its file name says $(file_stage "$STAGE_FILE")")
  PENDING+=("$1|$2")
}

for STAGE_FILE in e2e/lib/*.sh; do
  # shellcheck source=/dev/null
  . "$STAGE_FILE"
done
for STAGE_FILE in e2e/stages/*.sh; do
  registered=${#STAGES[@]}
  # shellcheck source=/dev/null
  . "$STAGE_FILE"
  [ "${#STAGES[@]}" -eq $((registered + 1)) ] || PROBLEMS+=("$STAGE_FILE must register exactly one stage")
done

# The plan's Verification list, item by item, "<item>|<what covers it>": the stages that check
# it, "ci" for CI jobs that are not stages, or "manual" for what CI cannot check (see
# docs/e2e-coverage.md). An item is covered when each of its stages is a real check.
VERIFICATION=(
  "images for amd64 and arm64 on ghcr, the stack started on an Ubuntu runner|ci"
  "setup page|setup-page"
  "login|login"
  "Small Seed|small-seed"
  "founder hires|hire"
  "bus message|bus"
  "live list and router pick|router"
  "echo chat|echo-chat"
  "event to task|event-to-task"
  "memory|memory"
  "plugin install|plugin-install"
  "agentctl parity|agentctl-parity"
  "allow list blocks and passes|wall-blocked wall-allowed"
  "budget wall and defence slice|budget-wall defence-slice"
  "freeze all and release|freeze-all"
  "the Mac walk-through|manual"
  "the Coolify deploy|manual"
  "the real-model run|manual"
)

# pending_reason <stage>: print why the stage is not a real check yet; false for a real one.
pending_reason() {
  local p
  for p in ${PENDING[@]+"${PENDING[@]}"}; do
    if [ "${p%%|*}" = "$1" ]; then
      printf '%s' "${p#*|}"
      return 0
    fi
  done
  return 1
}

# is_stage <stage>: the stage is registered.
is_stage() {
  local s
  for s in ${STAGES[@]+"${STAGES[@]}"}; do [ "${s%%|*}" = "$1" ] && return 0; done
  return 1
}

run_stage() {
  local fn="stage_${1//-/_}" why
  if ! is_stage "$1"; then
    echo "unknown stage: $1 (see: e2e/run.sh list)" >&2
    exit 2
  fi
  echo "== $1"
  if why="$(pending_reason "$1")"; then
    skip "$1" "not yet covered: $why"
  else
    "$fn"
  fi
}

# cmd_list: each stage with what it checks and whether it is a real check, then each item of the
# plan's Verification list with what covers it. It fails on a broken stage file, a stage that is
# neither a function nor pending or is both, a stage the e2e job does not run, a function defined
# twice, or an item that names no stage.
cmd_list() {
  local s name what why item covers stage pending total n dup
  for s in ${PROBLEMS[@]+"${PROBLEMS[@]}"}; do fail "list: $s"; done
  for dup in $(grep -hoE '^[A-Za-z_][A-Za-z0-9_]*\(\)' e2e/run.sh e2e/lib/*.sh e2e/stages/*.sh | sort | uniq -d); do
    fail "list: the function ${dup%()} is defined more than once in e2e/"
  done
  for dup in $(for s in ${STAGES[@]+"${STAGES[@]}"}; do printf '%s\n' "${s%%|*}"; done | sort | uniq -d); do
    fail "list: the stage $dup is registered more than once"
  done
  echo "Stages, in run order:"
  for s in ${STAGES[@]+"${STAGES[@]}"}; do
    name="${s%%|*}" what="${s#*|}"
    if why="$(pending_reason "$name")"; then
      printf '  %-16s not yet  %s\n  %-16s          why: %s\n' "$name" "$what" "" "$why"
      declare -F "stage_${name//-/_}" >/dev/null && fail "list: $name is pending and has a stage_${name//-/_} function"
    elif declare -F "stage_${name//-/_}" >/dev/null; then
      printf '  %-16s real     %s\n' "$name" "$what"
    else
      fail "list: $name has no stage_${name//-/_} function and is not pending"
    fi
    grep -Eq "^ +run: e2e/run\\.sh ${name}\$" .github/workflows/images.yml ||
      fail "list: the e2e job in .github/workflows/images.yml does not run $name"
  done
  echo
  echo "The plan's Verification list:"
  for item in "${VERIFICATION[@]}"; do
    covers="${item#*|}" item="${item%%|*}"
    case "$covers" in
      ci) printf '  covered      %s (CI jobs in .github/workflows/images.yml)\n' "$item" ;;
      manual) printf '  manual       %s (not covered by CI: manual; docs/e2e-coverage.md)\n' "$item" ;;
      *)
        pending="" total=0 n=0
        for stage in $covers; do
          total=$((total + 1))
          is_stage "$stage" || fail "list: \"$item\" names $stage, which is not a stage"
          if pending_reason "$stage" >/dev/null; then
            n=$((n + 1)) pending="${pending:+$pending }$stage"
          fi
        done
        if [ "$n" -eq 0 ]; then
          printf '  covered      %s (%s)\n' "$item" "$covers"
        elif [ "$n" -eq "$total" ]; then
          printf '  not yet      %s (%s)\n' "$item" "$covers"
        else
          printf '  not yet      %s (%s; not yet: %s)\n' "$item" "$covers" "$pending"
        fi
        ;;
    esac
  done
}

case "${1:-}" in
  up) cmd_up || exit 1 ;;
  down) cmd_down ;;
  list) cmd_list ;;
  all) for s in ${STAGES[@]+"${STAGES[@]}"}; do run_stage "${s%%|*}"; done ;;
  '' | -h | --help) sed -n '4,20p' "$0" | sed 's/^# \{0,1\}//' ;;
  *) for s in "$@"; do run_stage "$s"; done ;;
esac

if [ "$failures" -gt 0 ]; then
  echo "FAILURES: $failures"
  exit 1
fi
