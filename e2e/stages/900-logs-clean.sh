# SPDX-License-Identifier: MIT
# shellcheck shell=bash
#
# Logs clean: the last stage. After the whole scenario, the machine, edge and control logs hold no
# error and no warning, except the few the scenario causes on purpose with its negative checks.
# Each of those is listed below with the check that causes it. Anything else is a fault to fix,
# never a line to add here. The rescue stage recreates the machine, so its log starts there.

stage logs-clean "after the scenario, no service has logged an error or a warning it did not cause on purpose"

# What a fault looks like in a log: a warning or an error (text or JSON), or a crash.
LOGS_CLEAN_FAULT='level=(WARN|ERROR)|"level":"(WARN|ERROR)"|^panic: |fatal error: '

LOGS_CLEAN_SERVICE=()
LOGS_CLEAN_LINE=()
LOGS_CLEAN_WHY=()

# logs_clean_expect <service> <regex> <the check that causes it>: a warning the scenario causes.
logs_clean_expect() {
  LOGS_CLEAN_SERVICE+=("$1")
  LOGS_CLEAN_LINE+=("$2")
  LOGS_CLEAN_WHY+=("$3")
}

logs_clean_expect edge 'msg="audit: refused a token on the inner address" .*err="no bearer token"' \
  "audit-sink: a record with no token is refused"
logs_clean_expect edge 'msg="audit: refused a token on the inner address" .*err="gatewaytoken: malformed token"' \
  "audit-sink: a record with a forged token is refused"
logs_clean_expect control 'msg="control page: wrong setup code"' \
  "control: a wrong setup code is refused"
logs_clean_expect control 'msg="control page: refused a form from another site"' \
  "control: a form from another site is refused"
logs_clean_expect control "msg=\"control page: refused a form without this session's form token\"" \
  "control: a form without this session's token is refused"
logs_clean_expect control 'msg="control page: sign-in failed"' \
  "control: a wrong passphrase is refused"

# logs_clean_why <service> <line>: print the check that causes this line; false for a fault.
logs_clean_why() {
  local i
  for i in ${LOGS_CLEAN_LINE[@]+"${!LOGS_CLEAN_LINE[@]}"}; do
    if [ "${LOGS_CLEAN_SERVICE[$i]}" = "$1" ] && printf '%s' "$2" | grep -Eq -- "${LOGS_CLEAN_LINE[$i]}"; then
      printf '%s' "${LOGS_CLEAN_WHY[$i]}"
      return 0
    fi
  done
  return 1
}

# logs_clean_check <service>: every warning and error in the service's log is one the scenario
# causes on purpose. Prints each fault in full.
logs_clean_check() {
  local service="$1" line why faults=0 expected=0
  while IFS= read -r line; do
    if why="$(logs_clean_why "$service" "$line")"; then
      expected=$((expected + 1))
      printf '      expected (%s): %s\n' "$why" "$line"
    else
      faults=$((faults + 1))
      printf '      FAULT: %s\n' "$line"
    fi
  done < <(compose logs --no-color --no-log-prefix "$service" 2>&1 | grep -E -- "$LOGS_CLEAN_FAULT")
  if [ "$faults" -eq 0 ]; then
    pass "the $service log has no error or warning beyond the ${expected} the scenario causes"
  else
    fail "the $service log has ${faults} error or warning lines the scenario does not cause (listed above)"
  fi
}

stage_logs_clean() {
  local service
  for service in machine edge control; do
    needs logs-clean "$service" || continue
    logs_clean_check "$service"
  done
}
