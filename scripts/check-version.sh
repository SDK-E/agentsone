#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Keep the pinned image tag and the release version in step.
#
#   scripts/check-version.sh            VERSION holds a plain X.Y.Z, and every image in
#                                       compose.yaml and compose.coolify.yaml defaults to the
#                                       tag v<VERSION>
#   scripts/check-version.sh vX.Y.Z     the same, and the release tag vX.Y.Z is v<VERSION>
#
# AGENTSONE_ROOT points the script at another folder (the self-test uses that).
set -euo pipefail

if [ "$#" -gt 1 ]; then
  echo "usage: $0 [vX.Y.Z]" >&2
  exit 2
fi
release_tag="${1:-}"

root="${AGENTSONE_ROOT:-$(cd "$(dirname "$0")/.." && pwd)}"
cd "$root"

semver='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
failed=0
fail() {
  echo "check-version: $1" >&2
  failed=1
}

if [ ! -f VERSION ]; then
  echo "check-version: VERSION is missing" >&2
  exit 1
fi
version="$(cat VERSION)"
if ! [[ "$version" =~ $semver ]]; then
  echo "check-version: VERSION must hold one plain X.Y.Z (no v, no suffix), found: ${version}" >&2
  exit 1
fi
want="v${version}"

if [ -n "$release_tag" ] && [ "$release_tag" != "$want" ]; then
  fail "release tag ${release_tag} does not match VERSION ${version} (expected ${want})"
fi

for file in compose.yaml compose.coolify.yaml; do
  if [ ! -f "$file" ]; then
    fail "${file} is missing"
    continue
  fi
  images="$(grep -c 'image: ghcr.io/sdk-e/agentsone-' "$file" || true)"
  defaults="$(grep -c 'image: ghcr.io/sdk-e/agentsone-[a-z]*:[$]{AGENTSONE_IMAGE_TAG:-' "$file" || true)"
  if [ "$images" -eq 0 ]; then
    fail "${file} has no ghcr.io/sdk-e/agentsone-* image"
  fi
  if [ "$images" -ne "$defaults" ]; then
    fail "${file}: every agentsone image must use \${AGENTSONE_IMAGE_TAG:-${want}} ($images images, $defaults with the variable)"
  fi
  while IFS= read -r found; do
    found="${found#AGENTSONE_IMAGE_TAG:-}"
    if [ "$found" != "$want" ]; then
      fail "${file}: default image tag is ${found}, VERSION says ${want}"
    fi
  done < <(grep -o 'AGENTSONE_IMAGE_TAG:-[^}]*' "$file" | sort -u)
done

if [ "$failed" -ne 0 ]; then
  exit 1
fi
echo "check-version: VERSION ${version}, image tag ${want}${release_tag:+, release tag ${release_tag}} agree"
