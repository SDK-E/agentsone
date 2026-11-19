#!/bin/sh
# SPDX-License-Identifier: MIT
#
# Download one file listed in a pins file and check its SHA-256 before
# keeping it. Used by the image build for parts that do not come from Debian
# (Node.js, the VSCodium server).
#
# A pins file has one line per name and architecture; # starts a comment:
#
#   <name>  <arch>  <sha256>  <url>
#
# <arch> is the Docker platform name (amd64, arm64). The file is written to
# <out-file> only when the checksum matches; nothing is left behind otherwise.
#
# Usage: fetch-pinned.sh <pins-file> <name> <arch> <out-file>
set -eu

if [ "$#" -ne 4 ]; then
    echo "usage: fetch-pinned.sh <pins-file> <name> <arch> <out-file>" >&2
    exit 2
fi
pins=$1 name=$2 arch=$3 out=$4

rows=$(awk -v n="$name" -v a="$arch" '$1 !~ /^#/ && $1 == n && $2 == a' "$pins")
count=$(printf '%s' "$rows" | grep -c . || true)
if [ "$count" -eq 0 ]; then
    echo "fetch-pinned: no pin for $name $arch in $pins" >&2
    exit 1
fi
if [ "$count" -gt 1 ]; then
    echo "fetch-pinned: more than one pin for $name $arch in $pins" >&2
    exit 1
fi
sha=$(printf '%s\n' "$rows" | awk '{ print $3 }')
url=$(printf '%s\n' "$rows" | awk '{ print $4 }')

part=$out.part
trap 'rm -f "$part"' EXIT
if ! curl --fail --silent --show-error --location --retry 3 --proto '=https,file' \
    --output "$part" "$url"; then
    echo "fetch-pinned: download of $name $arch failed: $url" >&2
    exit 1
fi
if ! echo "$sha  $part" | sha256sum --check --status; then
    echo "fetch-pinned: checksum of $name $arch does not match the pin: $url" >&2
    exit 1
fi
mv -f "$part" "$out"
echo "fetch-pinned: $name $arch verified"
