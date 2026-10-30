#!/bin/sh
# Run every analyzer on application code; upstream pointer arithmetic has one exception.
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
packages=''
driver=''
for package in $(go list -tags "$tags" ./...); do
 case "$package" in
  */internal/thirdparty/sqlite) driver="$package" ;;
  *) packages="$packages $package" ;;
 esac
done
# Go import paths contain no shell whitespace.
# shellcheck disable=SC2086
go vet -tags "$tags" $packages
if [ -n "$driver" ]; then go vet -tags "$tags" -unsafeptr=false "$driver"; fi
