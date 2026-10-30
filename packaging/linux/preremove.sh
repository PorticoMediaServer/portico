#!/bin/sh
set -eu
# Only a removal stops and disables the service. Debian passes "remove" for a
# removal and "upgrade" (among others) otherwise; RPM passes how many copies of
# the package will remain: 0 for a removal, 1 or more for an upgrade. On an
# upgrade the service is left alone here and the new package's post-install
# script restarts it onto the new build.
case "${1:-}" in
  remove | 0 | '') ;;
  *) exit 0 ;;
esac
if command -v systemctl >/dev/null 2>&1; then
  systemctl stop portico-media-server.service || true
  systemctl disable portico-media-server.service || true
fi
