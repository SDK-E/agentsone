#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# check-test-drops: fail when new test code drops a Close or Remove error
# with a blank, such as `_ = f.Close()` or `defer func() { _ = os.Remove(p) }()`.
#
# Usage: scripts/check-test-drops.sh <base-rev>
#
# Only lines added since <base-rev> are checked, so old tests stay as they
# are. Test code is any _test.go file and any <package>test helper package.
# Use the coretest helpers instead: coretest.Close(t, f),
# coretest.Remove(t, path) or coretest.Check(t, cleanup).
#
# Exit status: 0 clean, 1 at least one new drop, 2 bad input.
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: scripts/check-test-drops.sh <base-rev>" >&2
  exit 2
fi
base="$1"
if ! git rev-parse --verify --quiet "$base^{commit}" > /dev/null; then
  echo "check-test-drops: unknown revision: $base" >&2
  exit 2
fi

git diff --unified=0 --no-color --no-ext-diff "$base" HEAD -- '*.go' | awk '
  /^\+\+\+ / { file = substr($0, 7); next }
  /^@@/ { split($3, a, ","); line = substr(a[1], 2) + 0; next }
  /^\+/ {
    code = substr($0, 2)
    if ((file ~ /_test\.go$/ || file ~ /(^|\/)[a-z]+test\//) &&
        code ~ /(^|[^[:alnum:]_])_[[:space:]]*=[[:space:]]*[^=].*\.(Close|Remove|RemoveAll)\(/) {
      printf "%s:%d: blank drops a Close or Remove error; use coretest.Close, coretest.Remove or coretest.Check\n", file, line
      bad = 1
    }
    line++
  }
  END { exit bad }
'
