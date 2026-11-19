#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Build the Go programs of the machine image as static binaries:
#
#   <out>/bin/agentsoned, <out>/bin/agentctl
#   <out>/runners/<name>    the one Go main under runners/<name>/
#   <out>/emitters/<name>   the one Go main under emitters/<name>/
#
# Folders that hold no Go main yet (a library, a TypeScript runner, a part
# still being built) are skipped with a note. GOOS and GOARCH pass through,
# so the build stage can cross-compile instead of emulating the target.
#
# Usage: build-go.sh <repo-dir> <out-dir>
set -euo pipefail

if (($# != 2)); then
    echo "usage: build-go.sh <repo-dir> <out-dir>" >&2
    exit 2
fi
mkdir -p "$2"
out=$(realpath "$2")
cd "$1"

go_build() {
    echo "build: $1 -> ${2#"$out"/}"
    CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags='-s -w' -o "$2" "$1"
}

# mains_under <dir>: import paths of the Go main packages under <dir>, if any.
mains_under() {
    [[ -n "$(find "$1" -name '*.go' -print -quit)" ]] || return 0
    go list -f '{{if eq .Name "main"}}{{.ImportPath}}{{end}}' "./$1/..."
}

for cmd in agentsoned agentctl; do
    mapfile -t mains < <([[ -d "cmd/$cmd" ]] && mains_under "cmd/$cmd")
    if ((${#mains[@]} == 0)); then
        echo "skip: cmd/$cmd has no Go main yet"
        continue
    fi
    go_build "./cmd/$cmd" "$out/bin/$cmd"
done

for kind in runners emitters; do
    for dir in "$kind"/*/; do
        [[ -d "$dir" ]] || continue
        dir=${dir%/}
        mapfile -t mains < <(mains_under "$dir")
        case ${#mains[@]} in
            0) echo "skip: $dir has no Go main" ;;
            1) go_build "${mains[0]}" "$out/$kind/$(basename "$dir")" ;;
            *)
                echo "$dir has more than one Go main: ${mains[*]}" >&2
                exit 1
                ;;
        esac
    done
done
