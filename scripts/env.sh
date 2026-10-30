#!/bin/sh
PORTICO_ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
export GOCACHE="${GOCACHE:-$PORTICO_ROOT/.cache/go-build}"
export GOMODCACHE="${GOMODCACHE:-$PORTICO_ROOT/.cache/go-mod}"
export TMPDIR="${TMPDIR:-$PORTICO_ROOT/.cache/tmp}"
# Keep local recipes on the module-pinned compiler, independent of PATH defaults.
export GOTOOLCHAIN=go$(sed -n 's/^go //p' "$PORTICO_ROOT/server/go.mod")
mkdir -p "$GOCACHE" "$GOMODCACHE" "$TMPDIR"
export npm_config_cache="$PORTICO_ROOT/.cache/npm"
export CP_HOME_DIR="$PORTICO_ROOT/.cache/cocoapods"
export DEVELOPER_DIR=/Applications/Xcode-beta.app/Contents/Developer
exec "$@"
