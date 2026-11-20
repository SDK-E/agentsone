#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Smoke test of a built machine image, run the way compose runs it: no
# privileges, no capabilities added, private cgroup namespace, named volumes,
# and an internal network with no way out, like the inner network.
# The gateway-key volume holds a throwaway key laid out as the edge writes
# it (folder 0700 and file 0600, both owned by the edge user 65532) and is
# mounted read-only, as in compose.yaml.
#
#   1. the container turns healthy (agentsoned answers on api.sock);
#   2. its log has no warning or error lines;
#   3. its log proves the network wall: no default route, one network;
#   4. it stops cleanly on SIGTERM (exit status 0);
#   5. after a restart machine.key in the data volume is unchanged.
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
gatewaykey="$name-gateway-key"
network="$name-inner"

cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker volume rm -f "$state" "$home" "$gatewaykey" >/dev/null 2>&1 || true
  docker network rm "$network" >/dev/null 2>&1 || true
}
trap cleanup EXIT

# The machine's only network is internal, as in compose.yaml: Docker gives it
# no gateway. On Docker's default bridge the daemon would find a default route
# and rightly refuse to wake agents.
docker network create --internal "$network" >/dev/null

# make_gateway_key fills the gateway-key volume the way the edge does
# (internal/edge/tokenkey, image/edge/Dockerfile): 32 random bytes in key.
make_gateway_key() {
  docker run --rm --network none --entrypoint sh -v "$gatewaykey:/gateway-key" "$image" -c \
    'umask 077 && head -c 32 /dev/urandom >/gateway-key/key &&
     chown 65532:65532 /gateway-key /gateway-key/key &&
     chmod 0700 /gateway-key && chmod 0600 /gateway-key/key'
}

start() {
  docker run -d --name "$name" \
    --cgroupns private \
    --security-opt writable-cgroups=true \
    --security-opt no-new-privileges=true \
    --stop-timeout 30 \
    --pids-limit 4096 \
    --network "$network" \
    -e AGENTSONE_EDGE_IP=10.77.42.2 \
    -v "$state:/var/lib/agentsone" \
    -v "$home:/home" \
    -v "$gatewaykey:/run/agentsone/gateway-key:ro" \
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

# The daemon's self-check of the network wall (internal/agents/supervisor):
# the line operators grep for when they cannot open a shell in the machine.
check_wall() {
  if ! docker logs "$name" 2>&1 | grep -q 'network wall holds: no default route, interfaces: eth0'; then
    echo "FAIL - container log does not prove the network wall:" >&2
    docker logs "$name" 2>&1 | grep -i 'network wall' >&2 || true
    exit 1
  fi
  echo "ok   - container log proves the network wall"
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

# check_licence_file: the image ships the engine licence text, with the pinned
# engine and version, and no acceptance (a human gives that, in core.db).
check_licence_file() {
  local file=/usr/lib/agentsone/engine/licence.json doc
  doc=$(docker run --rm --entrypoint cat "$image" "$file") || {
    echo "FAIL - the image has no $file" >&2
    exit 1
  }
  for key in engine version text; do
    if ! grep -Eq "\"$key\": \"[^\"]+\"" <<<"$doc"; then
      echo "FAIL - $file has no \"$key\":" >&2
      echo "$doc" >&2
      exit 1
    fi
  done
  if grep -Eqi '"accept' <<<"$doc"; then
    echo "FAIL - $file must not hold an acceptance" >&2
    exit 1
  fi
  echo "ok   - the image ships the engine licence text"
}

check_licence_file
make_gateway_key
start
wait_healthy
check_logs
check_wall
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
