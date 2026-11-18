#!/usr/bin/env bash
# Runs tests/rules.brs with the brs interpreter (fetched by npx at the version named here):
# the app's pure rules against what the other Portico apps and a reference QR library answer.
set -euo pipefail
cd "$(dirname "$0")/.."
out=$(npx --yes --package @rokucommunity/brs@0.47.6 brs tests/rules.brs source/Address.brs source/Present.brs source/Qr.brs source/Capabilities.brs 2>&1) || true
echo "$out" | tail -20
echo "$out" | grep -q '^PASSED [0-9]* FAILED 0$'
# The sign-in screen's own code with stand-in nodes: late answers from another server are dropped.
out=$(npx --yes --package @rokucommunity/brs@0.47.6 brs tests/signin.brs components/screens/SignInScreen.brs source/Address.brs 2>&1) || true
echo "$out" | tail -20
echo "$out" | grep -q '^PASSED *[0-9]* FAILED *0$'
