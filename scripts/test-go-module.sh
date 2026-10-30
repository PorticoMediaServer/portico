#!/bin/sh
# Run the complete module test inventory one bounded package at a time, so a hung
# or broken package is named with the tests it never reported and the rest of the
# inventory still produces results. Every package and every test still runs; vet
# remains this workflow's separate step.
set -eu
case "${1:-}" in
apikit|server) ;;
*) echo 'expected module: apikit or server' >&2; exit 2 ;;
esac
exec python3 "$(dirname "$0")/ci-affected.py" go-module --module "$1"
