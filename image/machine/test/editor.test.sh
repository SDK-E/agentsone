#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Tests for the built-in editor, the VSCodium server baked into the image.
# They start the server the way agentsoned does (internal/ide): as a human's
# own Linux user, on a Unix socket in a folder only that user may enter,
# under the human's base path, without the editor's own token. Then they
# install an extension from Open VSX, the extension store of the open build.
# They need the image and the internet, so CI runs them inside the built
# image:
#
#   docker run --rm -v "$PWD/image/machine:/src:ro" --entrypoint /src/test/editor.test.sh \
#     agentsone-machine:dev
#
# Usage: editor.test.sh
set -euo pipefail

dir=/opt/vscodium-server
bin=$dir/bin/codium-server
# A small extension that is on Open VSX.
ext=EditorConfig.EditorConfig
uid=1000
user=h_test
home=/home/humans/test
run=/run/agentsone/ide/test
sock=$run/server.sock
log=$run/server.log
page=$(mktemp)
pid=""

failures=0
pass() { echo "ok   - $1"; }
fail() {
  failures=$((failures + 1))
  echo "FAIL - $1" >&2
}
finish() {
  if [ -n "$pid" ]; then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  fi
  if [ "$failures" -ne 0 ] && [ -f "$log" ]; then
    echo "--- server log" >&2
    cat "$log" >&2
  fi
  rm -f "$page"
}
trap finish EXIT

# as_user runs a command as the human's user with a clean environment, as
# the daemon's launcher does.
as_user() {
  setpriv --reuid="$uid" --regid="$uid" --clear-groups \
    env -i HOME="$home" USER="$user" LOGNAME="$user" SHELL=/bin/bash LANG=C.UTF-8 \
    PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin "$@"
}

# --- the build and its licence ------------------------------------------------
if [ -x "$bin" ]; then pass "server program at $bin"; else
  fail "no server program at $bin"
  exit 1
fi
# The server archive carries no licence; the image ships VSCodium's, pinned in pins.txt.
licence=/usr/share/doc/vscodium-server/LICENSE
if [ -f "$licence" ] && head -n 1 "$licence" | grep -qx "MIT License"; then pass "build ships its MIT licence ($licence)"; else
  fail "no MIT licence at $licence"
fi
gallery=$(grep -o '"serviceUrl": *"[^"]*"' "$dir/product.json" || true)
case "$gallery" in
  *https://open-vsx.org/vscode/gallery*) pass "extension store is Open VSX" ;;
  *) fail "extension store is not Open VSX: $gallery" ;;
esac

# --- the human's user and private folders, as the daemon makes them -----------
groupadd --gid "$uid" "$user"
useradd --uid "$uid" --gid "$uid" --home-dir "$home" --shell /bin/bash --no-create-home --no-user-group "$user"
mkdir -p "$home" "$run"
chown "$uid:$uid" "$home" "$run"
chmod 0700 "$home" "$run"
chmod 0711 "$(dirname "$run")"

# --- start and answer under the base path ------------------------------------
as_user "$bin" --socket-path "$sock" --server-base-path /ide/test --without-connection-token \
  --default-folder "$home" --disable-telemetry >"$log" 2>&1 &
pid=$!
for _ in $(seq 1 120); do
  [ -S "$sock" ] && break
  if ! kill -0 "$pid" 2>/dev/null; then break; fi
  sleep 0.5
done
if [ -S "$sock" ]; then pass "server listens on its socket"; else
  fail "server did not open $sock"
  exit 1
fi
owner=$(stat -c %u "$sock")
if [ "$owner" = "$uid" ]; then pass "socket belongs to the human's user"; else fail "socket owned by uid $owner"; fi

headers=$(curl --silent --show-error --unix-socket "$sock" --dump-header - --output "$page" http://localhost/ide/test/)
status=$(printf '%s' "$headers" | head -n 1)
case "$status" in
  "HTTP/1.1 200"*) pass "serves the editor at /ide/test/" ;;
  *) fail "editor page answered: $status" ;;
esac
if grep -q "workbench" "$page"; then pass "page is the workbench"; else fail "page is not the workbench"; fi
if printf '%s' "$headers" | grep -qi '^x-frame-options:'; then
  fail "editor sends its own X-Frame-Options"
else
  pass "editor sends no X-Frame-Options (the proxy sets SAMEORIGIN)"
fi
if grep -Eiq "ignoring option|not supported|unknown option" "$log"; then fail "server refused an option"; else
  pass "server took every option"
fi

# --- another human cannot reach it, and it opens no port -----------------------
other_uid=1001
groupadd --gid "$other_uid" h_other
useradd --uid "$other_uid" --gid "$other_uid" --home-dir /home/humans/other --shell /bin/bash --no-create-home --no-user-group h_other
if setpriv --reuid="$other_uid" --regid="$other_uid" --clear-groups \
  curl --silent --max-time 5 --unix-socket "$sock" --output /dev/null http://localhost/ide/test/ 2>/dev/null; then
  fail "another human's user reached the editor's socket"
else
  pass "another human's user cannot reach the editor's socket"
fi
# The kernel's table of TCP sockets names each one's uid (column 8); state 0A
# is LISTEN. The human's user, the editor's only user, must listen on none.
listening=$(awk -v u="$uid" 'FNR > 1 && $4 == "0A" && $8 == u' /proc/net/tcp /proc/net/tcp6 2>/dev/null || true)
if [ -z "$listening" ]; then pass "editor listens on no TCP port"; else fail "editor listens on a TCP port: $listening"; fi
runs_as=$(awk '/^Uid:/ { print $2 }' "/proc/$pid/status")
if [ "$runs_as" = "$uid" ]; then pass "editor runs as the human's user ($user), not root"; else fail "editor runs as uid $runs_as"; fi

# --- an extension from Open VSX ------------------------------------------------
if as_user "$bin" --install-extension "$ext" >"$run/install.log" 2>&1; then pass "installs $ext from Open VSX"; else
  cat "$run/install.log" >&2
  fail "install of $ext failed"
fi
if as_user "$bin" --list-extensions | grep -qix "$ext"; then pass "$ext is installed for the human"; else
  fail "$ext not listed"
fi
if [ -d "$home/.vscodium-server/extensions" ] && [ "$(stat -c %u "$home/.vscodium-server/extensions")" = "$uid" ]; then
  pass "extensions live in the human's own home"
else
  fail "extensions folder missing or not the human's"
fi

if [ "$failures" -ne 0 ]; then
  echo "$failures check(s) failed" >&2
  exit 1
fi
echo "all editor checks passed"
