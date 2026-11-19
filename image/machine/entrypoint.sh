#!/bin/sh
# SPDX-License-Identifier: MIT
#
# Entrypoint of the Agents One machine container. Runs on every start, under
# tini, before the daemon:
#
#   1. makes sure the volume folders exist and belong to root;
#   2. creates machine.key on the first start (32 random bytes, mode 0600),
#      the root secret agentsoned derives its per-purpose keys from;
#   3. replaces itself with the command (agentsoned by default).
#
# It never replaces a non-empty key and never touches what lives inside the
# volumes (agent homes, databases). Agent users and the machine recipe are
# restored by agentsoned itself before any agent wakes.
#
# Usage: entrypoint <command> [args...]
set -eu

state=/var/lib/agentsone
secrets=$state/secrets
key=$secrets/machine.key

log() { echo "agentsone-entrypoint: $*" >&2; }

if [ "$#" -eq 0 ]; then
    echo "usage: entrypoint <command> [args...]" >&2
    exit 2
fi
if [ "$(id -u)" -ne 0 ]; then
    log "must run as root (the daemon creates agent users and drops to them)"
    exit 1
fi

# dir <mode> <path>: create the folder if missing, then set its owner and mode.
# Only the folder itself changes; its contents are left as they are.
dir() {
    mkdir -p "$2"
    chown root:root "$2"
    chmod "$1" "$2"
}

dir 0700 "$state"
dir 0700 "$secrets"
dir 0700 "$state/plugins"
dir 0700 "$state/audit"
dir 0755 /home
dir 0755 /home/agents

if [ ! -s "$key" ]; then
    (
        umask 077
        head -c 32 /dev/urandom >"$key.new"
    )
    mv -f "$key.new" "$key"
    log "created $key"
fi
chown root:root "$key"
chmod 0600 "$key"

exec "$@"
