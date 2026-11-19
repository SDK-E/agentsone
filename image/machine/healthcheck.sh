#!/bin/sh
# SPDX-License-Identifier: MIT
#
# Container health check: healthy once agentsoned answers GET /v1/setup on
# its Unix socket. That endpoint is public and cheap, and it is the first
# one the web door needs.
#
# 200 is the contract answer. 501 is accepted while the setup operation is
# not built yet: the daemon is up and serving the socket either way.
set -eu

socket=/run/agentsone/api.sock

code=$(curl --silent --output /dev/null --write-out '%{http_code}' --max-time 4 \
    --unix-socket "$socket" http://localhost/v1/setup) || true
case "$code" in
    200 | 501) exit 0 ;;
esac
echo "agentsoned did not answer GET /v1/setup on $socket (status ${code:-none})" >&2
exit 1
