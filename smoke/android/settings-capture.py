#!/usr/bin/env python3
"""Capture the owner Settings evidence on the phone emulator, once a phone APK and the
single fleet emulator are free.

    python3 smoke/android/settings-capture.py --serial emulator-5554 \
        --apk /path/app-google-debug.apk --out /path/scratch/settings-owner \
        --creds /path/private/creds.json --build 7776f835

Steps, matching the drawn screens rather than guessing:
  1. install, clear, launch, sign in to the demo server as the owner
  2. Settings -> Server group list            (owner-server#33 top)
  3. Dashboard row -> confirm the real owner Dashboard opens, then back
  4. Troubleshooting -> Messages tab          (owner-server#33)
  5. Feedback                                 (owner-server#33)
  6. Reports and Support tabs                 (owner-server#34)

Every step screenshots first and asserts second, so a failure still leaves evidence.
"""
import argparse
import json
import os
import re
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from flows import Device  # noqa: E402  (same lane, same helpers)

def actual_viewport(device):
    """Read the metrics the device really has instead of recording the size we meant to set."""
    size, _ = device.shell("wm", "size")
    density, _ = device.shell("wm", "density")
    size = re.sub(r"^Physical size:\s*", "", size).strip() or "unknown"
    density = re.sub(r"^Physical density:\s*", "", density).strip() or "unknown"
    return f"{size} at density {density}"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--serial", required=True)
    parser.add_argument("--apk", required=True)
    parser.add_argument("--out", required=True)
    parser.add_argument("--creds", required=True)
    parser.add_argument("--build", required=True)
    parser.add_argument("--server", default="https://demo.getportico.tv")
    args = parser.parse_args()

    os.makedirs(args.out, exist_ok=True)
    with open(args.creds) as handle:
        creds = json.load(handle)
    device = Device(args.serial, args.out)
    evidence = {"viewport": actual_viewport(device), "build": args.build, "steps": []}

    def note(name, path, ok=True, detail=""):
        evidence["steps"].append({"step": name, "screenshot": path, "ok": ok, "detail": detail})
        print(f"{'PASS' if ok else 'FAIL'} {name} | {path}" + (f" | {detail}" if detail else ""))

    def tap(name, *candidates):
        found = device.tap_contains(*candidates)
        time.sleep(2.5)
        return found

    def shot(name):
        path = device.screenshot(name)
        return path

    # 1. install and sign in
    output, code, error = device.adb("install", "-r", "-d", args.apk)
    if code != 0 or "Success" not in output:
        note("install", "", False, f"adb install failed: {output or error}")
        return 1
    device.adb("shell", "pm", "clear", "tv.getportico.portico")
    device.adb("shell", "am", "start", "-n", "tv.getportico.portico/.MainActivity")
    time.sleep(12)
    note("launch", shot("launch"))

    text = device.text()
    if "Sign in" in text or "Username" in text or "Use a local server" in text:
        if not (device.tap_id("accounts-welcome-local") or device.tap_contains("use a local server")):
            note("signin", shot("signin"), False, "no local-server option on the entry screen")
            return 1
        time.sleep(2)
        address_fields = device.fields()
        if address_fields:
            point = device.center(address_fields[0]["bounds"])
            device.shell("input", "tap", str(point[0]), str(point[1]))
            device.type_text(args.server)
            device.tap_contains("continue", "connect")
            time.sleep(6)
        if device.wait_text(r"Username|Password", 30) is None:
            note("signin", shot("signin"), False, f"no local form: {device.text()[:120]}")
            return 1
        # Each field is addressed by its own bounds, so the password cannot land in the username.
        fields = device.fields()
        if len(fields) < 2:
            note("signin", shot("signin"), False, f"only {len(fields)} fields drawn")
            return 1
        for index, value in ((0, creds["username"]), (1, creds["password"])):
            point = device.center(fields[index]["bounds"])
            device.shell("input", "tap", str(point[0]), str(point[1]))
            device.type_text(value)
            time.sleep(1)
        boxes = []
        tree = device.dump()
        if tree is not None:
            for node in tree.iter("node"):
                if (node.get("class") or "").endswith("Button"):
                    match = re.match(r"\[(\d+),(\d+)\]\[(\d+),(\d+)\]", node.get("bounds") or "")
                    if match:
                        boxes.append((int(match.group(2)), node.get("bounds")))
        password_y = device.center(fields[1]["bounds"])[1]
        below = [box for top, box in sorted(boxes) if top > password_y]
        if not below:
            note("signin", shot("signin"), False, "no primary button below the password field")
            return 1
        point = device.center(below[0])
        device.shell("input", "tap", str(point[0]), str(point[1]))
        device.wait_text(r"Who|Main profile|Home|Your servers", 40)
        if re.search(r"Who|Main profile", device.text(), re.I):
            device.tap_clickable_containing(creds["username"], "main profile")
            device.wait_text(r"Your servers|Home|Continue Watching|New on", 30)
        if re.search(r"Your servers", device.text(), re.I):
            device.tap_clickable_containing("portico demo")
            device.wait_text(r"Home|Continue Watching|New on", 30)
    home_text = device.text()
    signed_in = any(word in home_text for word in ("Home", "Continue Watching", "New on", "Your servers"))
    note("signed-in", shot("signed-in"), signed_in, home_text[:100].replace("\n", " | "))
    if not signed_in:
        return 1

    # 2. Settings -> Server group
    opened = tap("settings", "Settings")
    settings_text = device.text()
    server_rows = [line.strip() for line in settings_text.splitlines() if line.strip()]
    evidence["server_rows_in_drawn_order"] = server_rows[:40]
    server_group = device.tap_contains("Server", "This device")
    time.sleep(3)
    group_shot = shot("settings-server-group")
    note("settings-server-group", group_shot, bool(opened or server_group),
         f"visible: {settings_text[:120].replace(chr(10), ' | ')}")

    # 3. Dashboard row: does it open the real owner Dashboard?
    dashboard_open = device.tap_contains("Dashboard")
    time.sleep(4)
    dash_text = device.text()
    dash_shot = shot("dashboard")
    looks_real = any(word in dash_text for word in ("Plays", "Play history", "Activity", "Streams", "Bandwidth"))
    note("dashboard-opens", dash_shot, bool(dashboard_open) and looks_real,
         f"dashboard text: {dash_text[:140].replace(chr(10), ' | ')}")
    device.key("KEYCODE_BACK")
    time.sleep(2)

    # 4. Troubleshooting -> Messages
    opened_trouble = tap("troubleshooting", "Troubleshooting")
    messages = device.tap_contains("Messages")
    time.sleep(3)
    note("troubleshooting-messages", shot("troubleshooting-messages"), bool(opened_trouble and messages),
         f"troubleshooting text: {device.text()[:140].replace(chr(10), ' | ')}")

    # 5. Feedback
    feedback = device.tap_contains("Feedback")
    time.sleep(3)
    note("troubleshooting-feedback", shot("troubleshooting-feedback"), bool(feedback),
         f"visible: {device.text()[:120].replace(chr(10), ' | ')}")

    # 6. Reports and Support
    for tab in ("Reports", "Support"):
        tapped = device.tap_contains(tab)
        time.sleep(2.5)
        note(f"troubleshooting-{tab.lower()}", shot(f"troubleshooting-{tab.lower()}"), bool(tapped))

    device.adb("shell", "input", "keyevent", "KEYCODE_HOME")
    with open(os.path.join(args.out, "evidence.json"), "w") as handle:
        json.dump(evidence, handle, indent=2)
    print(f"\nevidence: {os.path.join(args.out, 'evidence.json')}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
