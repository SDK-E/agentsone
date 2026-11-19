#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Smoke test of a built machine image, run the way compose runs it: no
# privileges, no capabilities added, private cgroup namespace, named volumes.
#
#   1. the container turns healthy (agentsoned answers on api.sock);
#   2. its log has no warning or error lines;
#   3. it stops cleanly on SIGTERM (exit status 0);
#   4. after a restart machine.key in the data volume is unchanged.
#
# Everything it creates is removed on exit.
#
# Usage: image/ci/smoke.sh <image>
set -euo pipefail

if (($# != 1)); then
  echo "usage: image/ci/smoke.sh <image>" >&2
  exit 2
fi
image=$1
name="agentsone-smoke-$$"
state="$name-state"
home="$name-home"

cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker volume rm -f "$state" "$home" >/dev/null 2>&1 || true
}
trap cleanup EXIT

start() {
  docker run -d --name "$name" \
    --cgroupns private \
    --security-opt writable-cgroups=true \
    --security-opt no-new-privileges=true \
    --stop-timeout 30 \
    --pids-limit 4096 \
    -e AGENTSONE_EDGE_IP=10.77.42.2 \
    -v "$state:/var/lib/agentsone" \
    -v "$home:/home" \
    "$image" >/dev/null
}

wait_healthy() {
  local status
  for _ in $(seq 1 60); do
    status=$(docker inspect --format '{{.State.Health.Status}}' "$name")
    case "$status" in
      healthy)
        echo "ok   - container is healthy"
        return 0
        ;;
      unhealthy) break ;;
    esac
    sleep 1
  done
  echo "FAIL - container did not turn healthy (status $status)" >&2
  docker inspect --format '{{json .State.Health}}' "$name" >&2
  docker logs "$name" >&2
  exit 1
}

# The daemon logs with slog (level=WARN, "level":"ERROR"); tools may print
# "warning:" or "error:". Any of these fails the test.
check_logs() {
  local bad
  bad=$(docker logs "$name" 2>&1 | grep -Ei 'level=(warn|error)|"level":"(warn|error)"|\b(warning|error|fatal|panic)\b' || true)
  if [ -n "$bad" ]; then
    echo "FAIL - container log has warnings or errors:" >&2
    echo "$bad" >&2
    exit 1
  fi
  echo "ok   - container log has no warnings or errors"
}

stop_cleanly() {
  docker stop "$name" >/dev/null
  local code
  code=$(docker inspect --format '{{.State.ExitCode}}' "$name")
  if [ "$code" -ne 0 ]; then
    echo "FAIL - container exited with status $code on SIGTERM" >&2
    docker logs "$name" >&2
    exit 1
  fi
  echo "ok   - container stops cleanly on SIGTERM"
}

key_sum() {
  docker run --rm --entrypoint sha256sum -v "$state:/var/lib/agentsone:ro" "$image" \
    /var/lib/agentsone/secrets/machine.key
}

start
wait_healthy
check_logs
stop_cleanly
first=$(key_sum)

docker rm "$name" >/dev/null
start
wait_healthy
stop_cleanly
check_logs
if [ "$(key_sum)" = "$first" ]; then
  echo "ok   - machine.key survives a restart"
else
  echo "FAIL - machine.key changed across a restart" >&2
  exit 1
fi
echo "smoke test passed: $image"
