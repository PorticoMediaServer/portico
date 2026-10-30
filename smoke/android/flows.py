#!/usr/bin/env python3
"""M0 smoke: one Android emulator, phone or TV, driven through adb.

    python3 smoke/android/flows.py --serial emulator-5554 --apk /path/app.apk \
        --mode phone --out /path/scratch/android-phone

Steps follow M0-HANDS-ON.md core flows 1-5 and 10: install, cold launch, sign in,
Home, Library, Detail, Play, and on TV a D-pad focus walk with no trap.

The script never prints the password. It prints one line per check and writes
report.json next to the screenshots. A check that cannot run because the build or
the emulator lacks something is reported as BLOCKED, never as a pass.
"""
import argparse
import json
import os
import re
import subprocess
import sys
import time
import xml.etree.ElementTree as ElementTree

PACKAGE = "tv.getportico.portico"

# A drawn Home shows at least one of these rows. The shell's "Home" nav label alone is not enough:
# a screen stuck on "Loading, busy" can carry the nav bar with no content behind it.
HOME_ROWS = re.compile(r"Continue Watching|New on|On now|Listen", re.I)


def wait_for_home(device, timeout=90, poll=10):
    """Poll for real Home content and keep a screenshot plus the visible text at each stage.

    Returns (text or None, stages, elapsed). Stages are what classify a stall: a spinner, an error or
    an empty shell each look different in the evidence.
    """
    started = time.time()
    stages = []
    while time.time() - started < timeout:
        text = device.text()
        if HOME_ROWS.search(text):
            return text, stages, round(time.time() - started, 1)
        at = int(round(time.time() - started))
        stages.append({"at_s": at, "visible": text[:160],
                       "screenshot": device.screenshot(f"home-wait-{at}s")})
        time.sleep(poll)
    return None, stages, round(time.time() - started, 1)


class Device:
    def __init__(self, serial, out):
        self.serial = serial
        self.out = out
        self.shots = 0

    def shell(self, *args, timeout=60):
        result = subprocess.run(["adb", "-s", self.serial, "shell", *args],
                                capture_output=True, text=True, timeout=timeout)
        return result.stdout.strip(), result.returncode

    def adb(self, *args, timeout=300):
        result = subprocess.run(["adb", "-s", self.serial, *args],
                                capture_output=True, text=True, timeout=timeout)
        return result.stdout.strip(), result.returncode, result.stderr.strip()

    def screenshot(self, name):
        self.shots += 1
        path = os.path.join(self.out, f"{self.shots:02d}-{re.sub(r'[^a-z0-9-]+', '-', name.lower())}.png")
        subprocess.run(["adb", "-s", self.serial, "exec-out", "screencap", "-p"],
                       stdout=open(path, "wb"), timeout=120)
        return path

    def dump(self):
        self.shell("uiautomator", "dump", "/sdcard/smoke.xml")
        raw, code = self.shell("cat", "/sdcard/smoke.xml")
        if code != 0 or not raw.strip():
            return None
        try:
            return ElementTree.fromstring(raw)
        except ElementTree.ParseError:
            return None

    def nodes(self):
        tree = self.dump()
        if tree is None:
            return []
        found = []
        for node in tree.iter("node"):
            found.append({
                "text": (node.get("text") or "").strip(),
                "desc": (node.get("content-desc") or "").strip(),
                "clickable": node.get("clickable") == "true",
                "focused": node.get("focused") == "true",
                "bounds": node.get("bounds") or "",
                "class": node.get("class") or "",
            })
        return found

    @staticmethod
    def center(bounds):
        match = re.match(r"\[(\d+),(\d+)\]\[(\d+),(\d+)\]", bounds)
        if not match:
            return None
        left, top, right, bottom = (int(value) for value in match.groups())
        return (left + right) // 2, (top + bottom) // 2

    def tap_text(self, *candidates):
        for candidate in candidates:
            for node in self.nodes():
                if node["text"] == candidate or node["desc"] == candidate:
                    point = self.center(node["bounds"])
                    if point:
                        self.shell("input", "tap", str(point[0]), str(point[1]))
                        return candidate
        return None

    def tap_contains(self, *fragments):
        lowered = [fragment.lower() for fragment in fragments]
        for node in self.nodes():
            haystack = f"{node['text']} {node['desc']}".lower()
            if any(fragment in haystack for fragment in lowered):
                point = self.center(node["bounds"])
                if point:
                    self.shell("input", "tap", str(point[0]), str(point[1]))
                    return node["text"] or node["desc"]
        return None

    def tap_clickable_containing(self, *fragments):
        """Prefer a clickable node; fall back to any node with the text."""
        lowered = [fragment.lower() for fragment in fragments]
        for node in self.nodes():
            if not node["clickable"]:
                continue
            haystack = f"{node['text']} {node['desc']}".lower()
            if any(fragment in haystack for fragment in lowered):
                point = self.center(node["bounds"])
                if point:
                    self.shell("input", "tap", str(point[0]), str(point[1]))
                    return node["text"] or node["desc"]
        return self.tap_contains(*fragments)

    def type_text(self, value):
        escaped = value.replace(" ", "%s")
        self.shell("input", "text", escaped)

    def key(self, code, times=1):
        for _ in range(times):
            self.shell("input", "keyevent", code)

    def text(self):
        return "\n".join(node["text"] or node["desc"] for node in self.nodes() if node["text"] or node["desc"])

    def wait_text(self, pattern, timeout=30):
        """The uiautomator dump can lag a tap by a second or two; poll for the expected text."""
        deadline = time.time() + timeout
        while time.time() < deadline:
            current = self.text()
            if re.search(pattern, current, re.IGNORECASE):
                return current
            time.sleep(1.5)
        return None

    def tap_id(self, resource_id):
        tree = self.dump()
        if tree is None:
            return None
        for node in tree.iter("node"):
            if (node.get("resource-id") or "") == resource_id:
                point = self.center(node.get("bounds") or "")
                if point:
                    self.shell("input", "tap", str(point[0]), str(point[1]))
                    return resource_id
        return None

    def fields(self):
        tree = self.dump()
        if tree is None:
            return []
        found = []
        for node in tree.iter("node"):
            if (node.get("class") or "").endswith("EditText"):
                found.append({"bounds": node.get("bounds") or "", "desc": node.get("content-desc") or ""})
        return found

    def playing(self):
        """Real playback evidence, not a 204: the media session must report PLAYING."""
        out, _ = self.shell("dumpsys", "media_session")
        states = re.findall(r"state=PlaybackState \{state=(\d+)", out)
        return "3" in states, states[:5]


class Checks:
    def __init__(self, out, device=None):
        self.out = out
        self.device = device
        self.items = []

    def record(self, name, status, detail="", evidence=None):
        self.items.append({"id": name, "status": status, "detail": detail, "evidence": evidence or {}})
        print(f"{status.upper():7} {name}" + (f" | {detail}" if detail else ""))

    def failure_evidence(self, name):
        """Keep the screen a failure actually left behind, so it can be classified later."""
        if self.device is None:
            return {}
        try:
            return {"screenshot": self.device.screenshot("fail-" + name)}
        except Exception:  # noqa: BLE001 - evidence must never mask the real failure
            return {}

    def run(self, name, fn):
        started = time.time()
        try:
            detail, evidence = fn()
            self.record(name, "pass", detail or "", {**(evidence or {}), "seconds": round(time.time() - started, 2)})
        except AssertionError as error:
            self.record(name, "fail", str(error)[:600],
                        {"seconds": round(time.time() - started, 2), **self.failure_evidence(name)})
        except Exception as error:  # noqa: BLE001 - every failure must reach the report
            self.record(name, "fail", f"{type(error).__name__}: {error}"[:600],
                        {"seconds": round(time.time() - started, 2), **self.failure_evidence(name)})

    def blocked(self, name, reason):
        self.record(name, "blocked", reason)

    def write(self, meta):
        failed = [item for item in self.items if item["status"] == "fail"]
        payload = {**meta, "verdict": "fail" if failed else ("pass-with-blockers" if any(i["status"] == "blocked" for i in self.items) else "pass"),
                   "checks": self.items}
        path = os.path.join(self.out, "report.json")
        with open(path, "w") as handle:
            json.dump(payload, handle, indent=2)
        print(f"\n{payload['verdict'].upper()}: report at {path}")
        return 1 if failed else 0


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--serial", required=True)
    parser.add_argument("--apk", required=True)
    parser.add_argument("--mode", choices=["phone", "tv"], default="phone")
    parser.add_argument("--out", required=True)
    parser.add_argument("--server", default="https://demo.getportico.tv")
    parser.add_argument("--creds", required=True, help="private JSON with username and password")
    parser.add_argument("--build", default="unknown", help="build SHA for the report")
    parser.add_argument("--skip-playback", action="store_true", help="hold decode steps while Media owns readiness")
    parser.add_argument("--home-timeout", type=int, default=90,
                        help="seconds to wait for Home after sign-in, with staged evidence")
    parser.add_argument("--display", default="unset", help="actual wm size/density for the report")
    args = parser.parse_args()

    os.makedirs(args.out, exist_ok=True)
    with open(args.creds) as handle:
        creds = json.load(handle)
    device = Device(args.serial, args.out)
    checks = Checks(args.out, device)
    meta = {"surface": f"android-{args.mode}", "build": args.build, "serial": args.serial,
            "server": args.server, "display": args.display}

    def install():
        output, code, error = device.adb("install", "-r", "-d", args.apk)
        assert code == 0 and "Success" in output, f"adb install failed: {output or error}"
        device.adb("shell", "pm", "clear", PACKAGE)
        return "installed and cleared", {"screenshot": device.screenshot("installed")}

    def launch():
        device.adb("shell", "am", "start", "-n", f"{PACKAGE}/.MainActivity")
        time.sleep(12)
        text = device.text()
        shot = device.screenshot("launch")
        assert len(text) > 10 or os.path.getsize(shot) > 25000, f"blank launch screen; visible text {len(text)} chars"
        return f"{len(text)} chars of visible text", {"screenshot": shot}

    def sign_in():
        entry = device.wait_text(r"Welcome home|Sign in with Portico Account|Use a local server", 40)
        assert entry is not None, "the app drew no entry screen"
        # Prefer the drawn local-server control, by resource id when the build exposes one.
        if not (device.tap_id("accounts-welcome-local") or device.tap_contains("use a local server", "sign in directly to a server")):
            raise AssertionError("the entry screen draws no local-server option")
        assert device.wait_text(r"Server address|Use a local server", 20) is not None, "the server screen did not open"
        fields = device.fields()
        assert fields, "the server screen drew no address field"
        point = device.center(fields[0]["bounds"])
        device.shell("input", "tap", str(point[0]), str(point[1]))
        device.type_text(args.server)
        # The primary action under the field carries no text in the dump; tap the first button below it.
        tree = device.dump()
        buttons = []
        if tree is not None:
            for node in tree.iter("node"):
                if (node.get("class") or "").endswith("Button"):
                    box = node.get("bounds") or ""
                    match = re.match(r"\[(\d+),(\d+)\]\[(\d+),(\d+)\]", box)
                    if match:
                        buttons.append((int(match.group(2)), box))
        below = [box for top, box in sorted(buttons) if top > point[1]]
        assert below, "no primary button below the address field"
        target = device.center(below[0])
        device.shell("input", "tap", str(target[0]), str(target[1]))
        assert device.wait_text(r"Username|Password", 30) is not None, "the local sign-in form did not open"
        fields = device.fields()
        assert len(fields) >= 2, f"the local form drew {len(fields)} fields"
        for index, value in ((0, creds["username"]), (1, creds["password"])):
            point = device.center(fields[index]["bounds"])
            device.shell("input", "tap", str(point[0]), str(point[1]))
            device.type_text(value)
        tree = device.dump()
        buttons = []
        if tree is not None:
            for node in tree.iter("node"):
                text = (node.get("text") or "").strip()
                if (node.get("class") or "").endswith("Button") and text:
                    buttons.append((text, node.get("bounds") or ""))
        submit = next((box for text, box in buttons if re.search(r"sign in|continue", text, re.I)), None)
        if not submit:
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
            assert below, "no sign-in button below the password field"
            submit = below[0]
        point = device.center(submit)
        device.shell("input", "tap", str(point[0]), str(point[1]))
        assert device.wait_text(r"Who|Main profile|Home", 40) is not None, "sign-in did not reach the app"
        if re.search(r"Who|Main profile", device.text(), re.I):
            device.tap_clickable_containing(creds["username"], "main profile")
            device.wait_text(r"Your servers|Home|Continue Watching|New on", 30)
        # A server list may sit between the profile and Home.
        if re.search(r"Your servers", device.text(), re.I):
            device.tap_clickable_containing("portico demo")
            device.wait_text(r"Home|Continue Watching|New on", 30)
        # Home is the step that stalled on the previous pass. Poll past the old 40s window and keep a
        # screenshot plus the visible text at each stage, so a stall can be classified next time.
        drawn, stages, elapsed = wait_for_home(device, timeout=args.home_timeout)
        assert drawn is not None, (
            "Home drew no row within %ss after sign-in; stages %s"
            % (args.home_timeout, json.dumps(stages)[:800]))
        return f"signed in; Home rows drew after {elapsed}s; {len(drawn)} chars visible", {
            "screenshot": device.screenshot("after-signin"), "home_wait_stages": stages}

    def home():
        text = device.text()
        rows = [row for row in ("Continue Watching", "New on", "On now", "Listen", "Home") if row in text]
        assert len(rows) >= 2, f"Home shows no expected rows; visible: {text[:300]}"
        return f"rows: {', '.join(rows)}", {"screenshot": device.screenshot("home")}

    def library():
        opened = device.tap_contains("Movies", "Library", "Browse")
        assert opened, "no Movies or Library entry to open"
        time.sleep(6)
        text = device.text()
        assert len(text) > 20, "library screen stayed empty"
        return f"opened {opened}", {"screenshot": device.screenshot("library")}

    def detail():
        opened = device.tap_contains("Play", "More like this")
        if not opened:
            nodes = [node for node in device.nodes() if node["clickable"]]
            assert nodes, "library screen has no clickable card"
            point = device.center(nodes[0]["bounds"])
            device.shell("input", "tap", str(point[0]), str(point[1]))
        time.sleep(6)
        text = device.text()
        assert "Play" in text or "Resume" in text, f"detail screen has no Play control: {text[:200]}"
        return "detail screen with a play control", {"screenshot": device.screenshot("detail")}

    def play():
        assert device.tap_contains("play", "resume"), "no Play control on the detail screen"
        time.sleep(10)
        playing, states = device.playing()
        shot = device.screenshot("playing")
        if not playing:
            # Surface the real screen state rather than claiming success.
            assert False, f"media session does not report PLAYING (states {states}); screenshot {shot}"
        return f"media session PLAYING (states {states})", {"screenshot": shot}

    def focus_walk():
        assert args.mode == "tv", "focus walk is a TV check"
        seen = []
        for _ in range(12):
            device.key("KEYCODE_DPAD_RIGHT")
            time.sleep(0.4)
            focused = [node["text"] or node["desc"] for node in device.nodes() if node["focused"]]
            seen.append(focused[0] if focused else "")
        moves = len({name for name in seen if name})
        assert moves >= 3, f"D-pad right moved focus across only {moves} labelled controls: {seen}"
        return f"focus reached {moves} distinct controls in 12 presses", {"screenshot": device.screenshot("focus-walk"), "seen": seen}

    checks.run("install", install)
    checks.run("launch", launch)
    checks.run("signin", sign_in)
    checks.run("home", home)
    checks.run("library", library)
    checks.run("detail", detail)
    if args.skip_playback:
        checks.blocked("playback", "held: Media owns playback/decoder readiness for this window")
    else:
        checks.run("playback", play)
    if args.mode == "tv":
        checks.run("focus-walk", focus_walk)
    else:
        checks.blocked("focus-walk", "D-pad focus is a TV check")

    device.adb("shell", "am", "force-stop", PACKAGE)
    return checks.write(meta)


if __name__ == "__main__":
    sys.exit(main())
