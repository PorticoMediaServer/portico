#!/usr/bin/env bash
# Offline self-check for the Android pass harness.
#
#   bash smoke/android/self-check.sh
#
# Needs no device, no emulator and no credentials, so it can run on a VPS as the item gate for a
# smoke-harness change. It proves the scripts parse, their CLIs load, and the guards that keep a
# second emulator instance out of the way are still present.
set -uo pipefail
SELF="$(readlink -f "${BASH_SOURCE[0]}")"
ROOT="$(cd "$(dirname "$SELF")/../.." && pwd)"
fail=0
ok() { printf 'PASS %s\n' "$1"; }
bad() { printf 'FAIL %s\n' "$1"; fail=1; }

bash -n "$ROOT/smoke/android/m0-pass.sh" && ok "m0-pass.sh parses" || bad "m0-pass.sh does not parse"

python3 - "$ROOT" <<'PY' || fail=1
import ast, sys
root = sys.argv[1]
for name in ("flows.py", "settings-capture.py"):
    path = f"{root}/smoke/android/{name}"
    ast.parse(open(path).read(), path)
    print(f"PASS {name} parses")
PY

python3 "$ROOT/smoke/android/flows.py" --help >/dev/null && ok "flows.py CLI loads" || bad "flows.py CLI does not load"
python3 "$ROOT/smoke/android/settings-capture.py" --help >/dev/null && ok "settings-capture.py CLI loads" || bad "settings-capture.py CLI does not load"

python3 - "$ROOT" <<'PY' || fail=1
import sys
sys.path.insert(0, sys.argv[1] + "/smoke/android")
import flows


class Fake:
    def __init__(self, text):
        self.screen = text
        self.shots = []

    def text(self):
        return self.screen

    def screenshot(self, name):
        self.shots.append(name)
        return "/tmp/" + name + ".png"


stalled = Fake("Loading, busy\nHome  Search  Settings")
text, stages, _ = flows.wait_for_home(stalled, timeout=0.05, poll=0.01)
assert text is None, "a nav label without rows must not count as Home"
assert stages and "Loading" in stages[0]["visible"], stages
print("PASS a stalled Home records staged evidence, not a pass")

ready = Fake("Home\nContinue Watching\nNew on\nSearch")
text, stages, _ = flows.wait_for_home(ready, timeout=1, poll=0.01)
assert text and "Continue Watching" in text and stages == [], (text, stages)
print("PASS a drawn Home ends the wait at once")
PY

grep -q 'readlink -f "${BASH_SOURCE\[0\]}"' "$ROOT/smoke/android/m0-pass.sh" \
  && ok "the pass resolves its own root" || bad "the pass resolves its root from the caller"
grep -q 'a device or emulator is already attached' "$ROOT/smoke/android/m0-pass.sh" \
  && ok "the pass refuses an attached device" || bad "the pass may start a second instance"
grep -q 'qemu-system.\*-avd' "$ROOT/smoke/android/m0-pass.sh" \
  && ok "the pass refuses a live AVD process" || bad "the pass may start a second instance"
grep -q 'locks=' "$ROOT/smoke/android/m0-pass.sh" \
  && ok "the pass proves the wipe with device and lock counts" || bad "the wipe has no proof"
grep -q -- '--drawn-size' "$ROOT/smoke/android/m0-pass.sh" \
  && ok "the display override is opt-in" || bad "the display override is not selectable"
grep -q 'failure_evidence' "$ROOT/smoke/android/flows.py" \
  && ok "a failed check keeps its own screenshot" || bad "a failed check keeps no screenshot"
grep -q 'home_wait_stages' "$ROOT/smoke/android/flows.py" \
  && ok "a Home stall keeps staged evidence" || bad "a Home stall keeps no evidence"
grep -q 'def actual_viewport' "$ROOT/smoke/android/settings-capture.py" \
  && ok "the capture records the real viewport" || bad "the capture records a constant viewport"
grep -q 'server_rows_in_drawn_order' "$ROOT/smoke/android/settings-capture.py" \
  && ok "the capture records the Server row order" || bad "the capture records no row order"

# Exercise the actual stamp preflight in isolation; no adb or emulator command runs.
python3 - "$ROOT" <<'PYTEST' || fail=1
import os, pathlib, subprocess, sys, tempfile
source = pathlib.Path(sys.argv[1], "smoke/android/m0-pass.sh").read_text()
start = source.index("STAMP=$(curl")
end = source.index('say INFO "booting $AVD"', start)
assert start < source.index('nohup "$HOME/Android/Sdk/emulator/emulator"')
preflight = source[start:end]
with tempfile.TemporaryDirectory() as scratch:
    curl = pathlib.Path(scratch, "curl")
    curl.write_text('#!/bin/sh\nprintf "%s" "$TEST_BODY"\nexit "$TEST_CURL_EXIT"\n')
    curl.chmod(0o700)
    for name, body, curl_exit, success in (
        ("valid", '{"version":"test-build","id":"test-server"}', "0", True),
        ("unreadable", "upstream error", "0", False),
        ("missing identity", '{"version":"test-build"}', "0", False),
        ("wrong type", '[]', "0", False),
        ("HTTP failure", '{"version":"test-build","id":"test-server"}', "22", False),
    ):
        env = dict(os.environ, PATH=scratch + ":" + os.environ["PATH"], TEST_BODY=body, TEST_CURL_EXIT=curl_exit)
        run = subprocess.run(["bash", "-c", 'set -uo pipefail; say() { :; }; SERVER=https://configured.invalid;\n' + preflight + '\necho DEVICE_START_REACHED'], env=env, text=True, capture_output=True)
        assert (run.returncode == 0) == success, (name, run.returncode, run.stderr)
        assert ("DEVICE_START_REACHED" in run.stdout) == success, (name, run.stdout)
        print("PASS server preflight " + name)
PYTEST

printf 'self-check exit=%s\n' "$fail"
exit "$fail"
