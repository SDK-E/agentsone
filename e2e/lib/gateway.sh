# SPDX-License-Identifier: MIT
# shellcheck shell=bash
#
# Helpers for the stages that make model calls through the edge gateway: the e2e fake provider,
# the budget and the edge proof log. Sourced by e2e/run.sh before the stage files.

# model_call <token> [curl args...]: one chat completion from the machine to the gateway, as a
# runner sends it.
model_call() {
  local token="$1"
  shift
  in_machine curl -sS -m 10 -X POST -H "Authorization: Bearer ${token}" -H 'Content-Type: application/json' \
    -d '{"model":"autoagents/one","messages":[{"role":"user","content":"e2e"}]}' "$@" \
    "http://${EDGE_IP}:4000/v1/chat/completions"
}

# The provider e2e/fakeprovider/gateway.toml defines.
FAKE_PROVIDER="e2e-fake"
# What the fake answers (e2e/fakeprovider/main.go): the reason it gives its pick, its chat reply,
# and what each answer costs at the e2e prices, in micro-dollars. These and ROUTE_TEXT are read
# by the stage files, hence the SC2034 notes.
# shellcheck disable=SC2034
FAKE_ROUTE_REASON="Picked by the e2e fake provider."
# shellcheck disable=SC2034
FAKE_CHAT_REPLY="Hello from the e2e fake provider."
# shellcheck disable=SC2034
FAKE_COST=1100
# The request a human routes; the fake rates whatever the router shortlists.
# shellcheck disable=SC2034
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
