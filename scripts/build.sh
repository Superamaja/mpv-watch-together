#!/usr/bin/env sh
set -eu

repo_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$repo_root"

: "${GOCACHE:=$repo_root/.gocache}"
export GOCACHE

go run ./tools/build "$@"
