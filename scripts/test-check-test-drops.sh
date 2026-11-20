#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Self-test for scripts/check-test-drops.sh: new blank Close or Remove drops
# in test code fail, old ones and production code are left alone.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
check="$here/check-test-drops.sh"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

pass=0
failures=0

git -C "$work" init -q
git -C "$work" config user.email self-test@example.com
git -C "$work" config user.name self-test
mkdir -p "$work/pkg/pkgtest"

commit() {
  git -C "$work" add -A
  git -C "$work" commit -q -m "$1"
}

# expect <exit status> <name> <base-rev>
expect() {
  local want="$1" name="$2" status=0
  (cd "$work" && "$check" "$3") > "$work/out.txt" 2>&1 || status=$?
  if [ "$status" -eq "$want" ]; then
    pass=$((pass + 1))
  else
    echo "FAIL: $name expected exit $want, got $status" >&2
    cat "$work/out.txt" >&2
    failures=$((failures + 1))
  fi
}

printf 'package pkg\n\nfunc old() { _ = f.Close() }\n' > "$work/pkg/a_test.go"
commit base

printf 'package pkg\n\nfunc prod() { _ = f.Close() }\n' > "$work/pkg/prod.go"
printf 'package pkg\n\nfunc old() { _ = f.Close() }\n\nfunc checked(t T) { coretest.Close(t, f) }\n' > "$work/pkg/a_test.go"
commit clean
expect 0 old-drops-and-production-are-ignored HEAD~1

printf 'package pkg\n\nfunc n() {\n\tdefer func() { _ = os.Remove(p) }()\n}\n' > "$work/pkg/b_test.go"
commit test-drop
expect 1 new-remove-drop-in-a-test-fails HEAD~1
if ! grep -q '^pkg/b_test.go:4: ' "$work/out.txt"; then
  echo "FAIL: new-remove-drop-in-a-test-fails did not name pkg/b_test.go:4" >&2
  failures=$((failures + 1))
fi

printf 'package pkgtest\n\nfunc h() { _ = os.RemoveAll(d) }\n' > "$work/pkg/pkgtest/h.go"
commit helper-drop
expect 1 new-drop-in-a-helper-package-fails HEAD~1

printf 'package pkg\n\nfunc eq() { if a == b.Close() {} }\n' > "$work/pkg/c_test.go"
commit comparison
expect 0 a-comparison-is-not-a-blank HEAD~1

expect 2 unknown-revision-is-bad-input no-such-revision

if [ "$failures" -ne 0 ]; then
  echo "check-test-drops self-test: $failures failed, $pass passed" >&2
  exit 1
fi
echo "check-test-drops self-test: $pass checks passed"
