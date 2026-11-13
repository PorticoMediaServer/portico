#!/bin/sh
# Run every analyzer with the same capability tags as the application build.
# The same capability tags as the build, so tagged-only code is analyzed rather
# than skipped. Official CI exports PORTICO_GO_TAGS; a direct run reads the one
# checked list.
set -eu
if [ "${1:-}" = --tags ]; then
 [ "$#" -eq 2 ] || { echo 'usage: vet-go.sh [--tags TAGS]' >&2; exit 2; }
 tags=$2
else
 tags="${PORTICO_GO_TAGS:-$(python3 "$(dirname "$0")/release-build-tags.py" --capability)}"
fi
go vet -tags "$tags" ./...
