# SPDX-License-Identifier: MIT
# shellcheck shell=bash
#
# Helpers for stages that act as the first human and as agents with a token, without an agent
# runtime: the human signs in through the web door, and an agent is a bearer token the human
# issued for it, used from inside the machine as a tool would use it.
#
# Needs from run.sh: fail, pass, from_browser, in_machine, door, WORK, HUMAN, DOOR.

# human_signin <stage>: sign the first human in through the web door with this run's cookie jar.
# Fails the stage (and returns 1) when setup-page has not run or the sign-in is refused.
human_signin() {
  local stage="$1" out status
  if [ ! -s "$WORK/login.json" ]; then
    fail "$stage: no first human; run the setup-page stage first"
    return 1
  fi
  rm -f "$WORK/cookies"
  out="$(from_browser POST /v1/session -d @"$WORK/login.json" -w '\n%{http_code}' 2>&1)"
  status="${out##*$'\n'}"
  if [ "$status" != 201 ]; then
    fail "$stage: the first human cannot sign in (got $status)" "${out%$'\n'*}"
    return 1
  fi
}

# human_json <label> <status> <jq filter> <curl command...>: the command answers with exactly
# that status and a JSON body for which the jq filter is true (an empty filter checks the status
# only, for an answer with no body). The command must be a curl call; `-w` is added to read the
# status.
human_json() {
  local label="$1" want_status="$2" filter="$3" out status body
  shift 3
  out="$("$@" -w '\n%{http_code}' 2>&1)"
  status="${out##*$'\n'}"
  body="${out%$'\n'*}"
  if [ "$status" = "$want_status" ] && { [ -z "$filter" ] || printf '%s' "$body" | jq -e "$filter" >/dev/null 2>&1; }; then
    pass "$label ($status)"
  else
    fail "$label (got $status, wanted $want_status and: $filter)" "$body"
  fi
}

# human_token <agent> <scopes as a JSON array>: print a short-lived token for the agent, issued
# by the signed-in human. Prints nothing and returns 1 when the daemon refuses.
human_token() {
  local agent="$1" scopes="$2" body out status
  body="$(jq -nc --arg l "e2e $agent" --argjson s "$scopes" '{label: $l, scopes: $s, ttl_seconds: 600}')"
  out="$(from_browser POST "/v1/agents/${agent}/tokens" -d "$body" -w '\n%{http_code}' 2>&1)"
  status="${out##*$'\n'}"
  [ "$status" = 201 ] || return 1
  printf '%s' "${out%$'\n'*}" | jq -er '.token'
}

# human_as_agent <token> <method> <path> [curl args...]: a request to the daemon from inside the
# machine, as a tool holding an agent's token sends it. The path starts below /v1.
human_as_agent() {
  local token="$1" method="$2" path="$3"
  shift 3
  in_machine curl -sS -m 10 -X "$method" -H "Authorization: Bearer ${token}" \
    -H 'Content-Type: application/json' "$@" "http://127.0.0.1:7700/v1${path}"
}

# human_agent_present <agent>: true when the agent exists. For wait_for.
human_agent_present() {
  [ "$(from_browser GET "/v1/agents/$1" -o /dev/null -w '%{http_code}')" = 200 ]
}
