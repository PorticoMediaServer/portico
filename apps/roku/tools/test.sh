#!/usr/bin/env bash
# Runs tests/rules.brs with the brs interpreter (fetched by npx at the version named here):
# the app's pure rules against what the other Portico apps and a reference QR library answer.
set -euo pipefail
cd "$(dirname "$0")/.."
out=$(npx --yes --package @rokucommunity/brs@0.47.6 brs tests/rules.brs source/Address.brs source/Present.brs source/Qr.brs \
  source/Capabilities.brs source/Settings.brs source/Mdns.brs source/Server.brs source/ServerPages.brs source/AppLog.brs 2>&1) || true
echo "$out" | tail -20
echo "$out" | grep -q '^PASSED [0-9]* FAILED 0$'
# The renewal's server check (security finding F9): the refresh token goes only to the server whose id
# was kept at sign-in, and the scene keeps that id. Task and scene code with stand-ins; no network.
out=$(npx --yes --package @rokucommunity/brs@0.47.6 brs tests/serverid.brs components/tasks/ApiTask.brs 2>&1) || true
echo "$out" | tail -20
echo "$out" | grep -q '^PASSED *[0-9]* FAILED *0$'
out=$(npx --yes --package @rokucommunity/brs@0.47.6 brs tests/scene.brs components/MainScene.brs 2>&1) || true
echo "$out" | tail -20
echo "$out" | grep -q '^PASSED *[0-9]* FAILED *0$'
# kit/ListRow's own code with stand-in nodes: a row whose model leaves out a key it does not use lays out.
out=$(npx --yes --package @rokucommunity/brs@0.47.6 brs tests/listrow.brs components/kit/ListRow.brs 2>&1) || true
echo "$out" | tail -20
echo "$out" | grep -q '^PASSED *[0-9]* FAILED *0$'
# The sign-in screen's own code with stand-in nodes: late answers from another server are dropped.
out=$(npx --yes --package @rokucommunity/brs@0.47.6 brs tests/signin.brs components/screens/SignInScreen.brs source/Address.brs 2>&1) || true
echo "$out" | tail -20
echo "$out" | grep -q '^PASSED *[0-9]* FAILED *0$'
# Live TV's rules (source/Live.brs): the guide's time grid, its filters, the red dot and the rest.
out=$(npx --yes --package @rokucommunity/brs@0.47.6 brs tests/live.brs source/Live.brs source/Present.brs 2>&1) || true
echo "$out" | tail -20
echo "$out" | grep -q '^PASSED [0-9]* FAILED 0$'
# Watch Together's rules: codes, who may control, keeping in step, what a newer room changes.
out=$(npx --yes --package @rokucommunity/brs@0.47.6 brs tests/together.brs source/Together.brs 2>&1) || true
echo "$out" | tail -20
echo "$out" | grep -q '^PASSED *[0-9]* FAILED *0$'
# Every frame number, colour and type role the app reads is in source/Theme.brs. BrightScript only
# finds a missing one when the screen that reads it opens, and then the app stops.
python3 tools/theme-check.py
# The open-source licences the app lists are made from the files the package holds (tools/licences.py).
python3 tools/licences.py --check
