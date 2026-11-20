# SPDX-License-Identifier: MIT
# shellcheck shell=bash

stage event-to-task "an event that matches a trigger creates a task, and an event that does not match creates none"

# event_to_task_count <trigger id> <title>: how many tasks the trigger made with that title.
event_to_task_count() {
  from_browser GET '/v1/tasks?assignee=founder&limit=200' |
    jq -r --arg t "$1" --arg title "$2" '[.items[] | select(.trigger_id == $t and .title == $title)] | length'
}

# event_to_task_made <trigger id> <title>: true once the trigger has made one task. For wait_for.
event_to_task_made() { [ "$(event_to_task_count "$1" "$2")" = 1 ]; }

stage_event_to_task() {
  needs event-to-task machine edge || return 0
  door || return 0
  human_signin event-to-task || return 0
  local run type other subject title trigger out
  run="$(openssl rand -hex 4)"
  type="agentsone.e2e.disk-full"
  other="agentsone.e2e.disk-fine"
  subject="/var-${run}"
  title="Free disk: ${subject}"
  if ! out="$(wait_for 60 human_agent_present founder)"; then
    fail "event-to-task: the founder is not there 60 seconds after setup" "$out"
    return 0
  fi

  trigger="$(from_browser POST /v1/triggers -d "$(jq -nc --arg ty "$type" --arg ti "Free disk: {{subject}}" --arg n "e2e disk ${run}" \
    '{name: $n, filter: {types: [$ty]}, action: {kind: "create-task", assignee: "founder", title: $ti, priority: "normal"}, wake_rate: {max: 5, per_seconds: 60}}')" |
    jq -er 'select(.enabled == true and .fired_count == 0 and .action.kind == "create-task") | .id')" || trigger=""
  if [ -z "$trigger" ]; then
    fail "a human creates a trigger that makes a task for the founder on a disk event"
    return 0
  fi
  pass "a human creates a trigger that makes a task for the founder on a disk event ($trigger)"

  human_json "an event of another type is accepted" 201 ".type == \"${other}\"" \
    from_browser POST /v1/events -d "$(jq -nc --arg t "$other" --arg s "$subject" '{type: $t, subject: $s, data: {}}')"
  human_json "an event of the trigger's type is accepted" 201 ".type == \"${type}\" and .subject == \"${subject}\"" \
    from_browser POST /v1/events -d "$(jq -nc --arg t "$type" --arg s "$subject" '{type: $t, subject: $s, data: {}}')"
  if out="$(wait_for 30 event_to_task_made "$trigger" "$title")"; then
    pass "the matching event made one task, titled from the event's subject"
  else
    fail "the matching event made one task, titled from the event's subject" "$out"
    return 0
  fi
  human_json "the task is open, for the founder, and names the trigger" 200 \
    "[.items[] | select(.trigger_id == \"${trigger}\")] | length == 1 and (.items[0] | .status == \"open\" and .assignee == \"founder\" and .title == \"${title}\")" \
    from_browser GET '/v1/tasks?assignee=founder&limit=200'
  human_json "the trigger counts exactly one firing: the other event did not match" 200 '.fired_count == 1' \
    from_browser GET "/v1/triggers/${trigger}"

  human_json "a human deletes the trigger" 204 '' from_browser DELETE "/v1/triggers/${trigger}"
  human_json "after the delete an event of the type makes no new task" 201 ".type == \"${type}\"" \
    from_browser POST /v1/events -d "$(jq -nc --arg t "$type" --arg s "${subject}-after" '{type: $t, subject: $s, data: {}}')"
  human_json "the trigger is gone" 404 '.status == 404' from_browser GET "/v1/triggers/${trigger}"
  human_json "no task came from the deleted trigger's second event" 200 \
    "[.items[] | select(.title == \"Free disk: ${subject}-after\")] | length == 0" \
    from_browser GET '/v1/tasks?assignee=founder&limit=200'
}
