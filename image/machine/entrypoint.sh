#!/bin/sh
# SPDX-License-Identifier: MIT
#
# Entrypoint of the Agents One machine container. Runs on every start, under
# tini, before the daemon:
#
#   1. writes /etc/agentsone/config.toml: the image template, plus, when
#      compose sets AGENTSONE_EDGE_IP (one IPv4 or IPv6 address), the edge
#      address as the daemon's only trusted proxy and the edge audit sink
#      (port 7710) every audit record is shipped to;
#   2. makes sure the volume folders exist and belong to root, and that
#      /home/shared (where agents hand files to each other) is open to all,
#      with the sticky bit so nobody removes another agent's files;
#   3. creates machine.key on the first start (32 random bytes, mode 0600),
#      the root secret agentsoned derives its per-purpose keys from;
#   4. replaces itself with the command (agentsoned by default).
#
# When an agent or human asks for a reboot, agentsoned exits with code 75.
# tini passes it on and the compose restart policy (unless-stopped) starts
# the same container again; only a rescue (recreate) resets it to the image.
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
template=/usr/lib/agentsone/config.toml
config=/etc/agentsone/config.toml
edge_ip=${AGENTSONE_EDGE_IP:-}

log() { echo "agentsone-entrypoint: $*" >&2; }

if [ "$#" -eq 0 ]; then
    echo "usage: entrypoint <command> [args...]" >&2
    exit 2
fi
if [ "$(id -u)" -ne 0 ]; then
    log "must run as root (the daemon creates agent users and drops to them)"
    exit 1
fi

# is_ip <value>: a plain IPv4 address (four numbers up to 255) or an IPv6
# address (hex digits and colons). Anything else, CIDR ranges included, is
# refused, so the value can never break out of its TOML string.
is_ip() {
    case "$1" in
        *[!0-9.]*) ;;
        *.*.*.*)
            printf '%s\n' "$1" | awk -F. 'NF == 4 && $1 <= 255 && $2 <= 255 && $3 <= 255 && $4 <= 255 &&
                $1 != "" && $2 != "" && $3 != "" && $4 != "" { ok = 1 } END { exit !ok }'
            return
            ;;
        *) return 1 ;;
    esac
    case "$1" in
        *[!0-9A-Fa-f:]* | *:::*) return 1 ;;
        *:*:*) return 0 ;;
        *) return 1 ;;
    esac
}

if [ -n "$edge_ip" ] && ! is_ip "$edge_ip"; then
    log "AGENTSONE_EDGE_IP must be one IPv4 or IPv6 address, got '$edge_ip'"
    exit 1
fi

# Written whole on every start, so a restart never stacks tables.
mkdir -p "$(dirname "$config")"
cp "$template" "$config.new"
if [ -n "$edge_ip" ]; then
    printf '\n[edge]\n# The web door in edge, from AGENTSONE_EDGE_IP.\ntrusted_proxies = ["%s"]\n' "$edge_ip" >>"$config.new"
    sink_host=$edge_ip
    case "$edge_ip" in *:*) sink_host="[$edge_ip]" ;; esac
    printf '\n[audit]\n# The edge audit sink, from AGENTSONE_EDGE_IP: every record is shipped there as written.\nedge_sink = "http://%s:7710"\n' "$sink_host" >>"$config.new"
fi
chown root:root "$config.new"
chmod 0644 "$config.new"
mv -f "$config.new" "$config"

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
dir 1777 /home/shared

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
