#!/bin/sh
# CI guard: a default build must contain no embedded development root.
#
# The Hosted trust contract has two copies: the server's (server/internal/hostedtrust)
# and Hosted's, in the private portico-internal repository
# (hosted-services/hosted/trust). When that checkout is beside this one (or
# PORTICO_INTERNAL_REPO names it) the two must match byte for byte; public CI
# has no access to it and checks the server alone.
set -eu
repo=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "${tmp:?}"' EXIT HUP INT TERM
server_trust="$repo/server/internal/hostedtrust"
internal="${PORTICO_INTERNAL_REPO:-$repo/../../portico-internal}"
hosted_trust="$internal/hosted-services/hosted/trust"
if [ -d "$hosted_trust" ]; then
  (cd "$hosted_trust" && find . -type f | sort) > "$tmp/hosted-files"
  (cd "$server_trust" && find . -type f | sort) > "$tmp/server-files"
  if ! cmp -s "$tmp/hosted-files" "$tmp/server-files"; then
    diff "$tmp/hosted-files" "$tmp/server-files" >&2 || true
    echo 'trust copies have different files' >&2; exit 1
  fi
  while IFS= read -r file; do
    cmp "$hosted_trust/$file" "$server_trust/$file"
  done < "$tmp/hosted-files"
else
  echo "No portico-internal checkout at $internal: comparing the server's trust copy only."
fi
# The release root inspector repeats the development-root digest; it must match.
digest=$(sed -n 's/.*hex.EncodeToString(sum\[:\]) == "\([0-9a-f]\{64\}\)".*/\1/p' "$server_trust/trust.go")
[ -n "$digest" ] && grep -q "$digest" "$repo/scripts/inspect-hosted-root.go" || { echo 'inspect-hosted-root.go development-root digest differs from trust.go' >&2; exit 1; }
(cd "$repo/server" && GOWORK="$repo/go.work" "$repo/scripts/env.sh" go build -o "$tmp/server" ./cmd/server)
root=$(sed -n 's/.*DevelopmentRoot = "\([^"]*\)".*/\1/p' "$server_trust/development_root.go")
[ -n "$root" ] || exit 1
if grep -a -q "$root" "$tmp/server"; then echo 'untagged server embeds development root' >&2; exit 1; fi
(cd "$repo/server" && GOWORK="$repo/go.work" "$repo/scripts/env.sh" go test ./internal/hostedtrust ./internal/hosted -run 'TestUntaggedTrust' -count=1)
