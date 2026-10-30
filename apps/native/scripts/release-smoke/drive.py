#!/usr/bin/env python3
"""O-158 release boot smoke: one emulator, one release APK, one throwaway server.

    python3 drive.py --serial emulator-5554 --apk app-google-release.apk \
        --fixture <work>/fixture.json --out <dir>

Installs the release (Hermes) APK on a clean emulator, signs in to the fixture server through the
real UI, and asserts Home draws. It then force-stops the app and cold-launches it again: the
signed-in start is the path the 28 Oct crypto.getRandomValues P0 crashed on. Any fatal in logcat
(a native crash, an uncaught JS exception, the process dying) fails the run.

The password is read from fixture.json and is never printed. Evidence (screens, logcat, visible
text) goes to --out; the last stdout line is a JSON summary.
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
# Home with rows, or a fresh server's Home with nothing scanned yet. Either one means the shell and
# the Home destination drew; the entry and sign-in screens never carry these words.
HOME = re.compile(r"Continue Watching|New on|Nothing here yet|Titles appear here|is getting ready", re.I)
FATAL = re.compile(r"FATAL EXCEPTION|JavascriptException|Fatal signal \d+|ANR in tv\.getportico|"
                   r"com\.facebook\.react\.common\.JavascriptException|Unhandled JS Exception", re.I)


class Device:
    def __init__(self, serial, out):
        self.serial, self.out, self.shots = serial, out, 0

    def adb(self, *args, timeout=300):
        result = subprocess.run(["adb", "-s", self.serial, *args], capture_output=True, text=True, timeout=timeout)
        return result.stdout.strip(), result.returncode, result.stderr.strip()

    def shell(self, *args, timeout=60):
        return self.adb("shell", *args, timeout=timeout)[0]

    def screenshot(self, name):
        self.shots += 1
        path = os.path.join(self.out, f"{self.shots:02d}-{name}.png")
        with open(path, "wb") as handle:
            subprocess.run(["adb", "-s", self.serial, "exec-out", "screencap", "-p"], stdout=handle, timeout=60)
        return path

    def dump(self):
        self.shell("uiautomator", "dump", "/sdcard/ui.xml", timeout=60)
        raw = self.shell("cat", "/sdcard/ui.xml")
        try:
            return ElementTree.fromstring(raw[raw.find("<"):]) if "<" in raw else None
        except ElementTree.ParseError:
            return None

    def nodes(self):
        tree = self.dump()
        return [] if tree is None else [{
            "text": (node.get("text") or "").strip(), "desc": (node.get("content-desc") or "").strip(),
            "id": node.get("resource-id") or "", "class": node.get("class") or "", "bounds": node.get("bounds") or "",
            "clickable": node.get("clickable") == "true",
        } for node in tree.iter("node")]

    def text(self):
        return " | ".join(value for node in self.nodes() for value in (node["text"], node["desc"]) if value)

    @staticmethod
    def center(bounds):
        match = re.match(r"\[(\d+),(\d+)\]\[(\d+),(\d+)\]", bounds)
        if not match:
            return None
        left, top, right, bottom = map(int, match.groups())
        return (left + right) // 2, (top + bottom) // 2

    def tap(self, bounds):
        point = self.center(bounds)
        assert point, f"no bounds to tap: {bounds!r}"
        self.shell("input", "tap", str(point[0]), str(point[1]))

    def tap_where(self, predicate):
        for node in self.nodes():
            if predicate(node) and self.center(node["bounds"]):
                self.tap(node["bounds"])
                return True
        return False

    def type_text(self, value):
        # `input text` takes %s for a space; the fixture's values never contain one.
        self.shell("input", "text", value.replace(" ", "%s"))

    def wait_text(self, pattern, timeout):
        started = time.time()
        while time.time() - started < timeout:
            text = self.text()
            if re.search(pattern, text, re.I):
                return text
            self.dismiss_system_dialog(text)
            time.sleep(2)
        return None

    def dismiss_system_dialog(self, text):
        # A cold emulator often shows "System UI isn't responding" over the app. It is the
        # emulator's, never the app's: our own ANR is caught from logcat. Wait it out.
        if re.search(r"System UI isn.t responding|isn.t responding", text, re.I) and not re.search(r"Portico isn.t responding", text, re.I):
            self.tap_where(lambda node: node["text"].lower() == "wait")

    def fields(self):
        return [node for node in self.nodes() if node["class"].endswith("EditText")]

    def buttons_below(self, y):
        boxes = []
        for node in self.nodes():
            if node["class"].endswith("Button") or node["clickable"]:
                point = self.center(node["bounds"])
                if point and point[1] > y:
                    boxes.append((point[1], node))
        return [node for _, node in sorted(boxes, key=lambda item: item[0])]

    def alive(self):
        return bool(self.shell("pidof", PACKAGE))

    def idle(self, timeout=30):
        """Wait until Android has really finished with the package: no process and the package in
        its stopped state, unchanged for a moment. `pm clear` and `am force-stop` return before
        the platform is done; launching earlier lets their asynchronous kill land on the launch."""
        deadline, steady = time.time() + timeout, 0
        while time.time() < deadline:
            state = self.shell("dumpsys", "package", PACKAGE)
            # Older images may not print the field; then process absence alone decides.
            stopped = "stopped=true" in state or "stopped=" not in state
            steady = steady + 1 if stopped and not self.alive() else 0
            if steady >= 3:
                return
            time.sleep(0.5)
        raise AssertionError(f"the package did not settle within {timeout} s")

    def quiesce(self):
        """Force-stop, then wait until the package is idle."""
        self.adb("shell", "am", "force-stop", PACKAGE)
        self.idle()


def fatal_lines(device):
    log, _, _ = device.adb("logcat", "-d", "-b", "main,system,crash", timeout=120)
    return [line for line in log.splitlines() if FATAL.search(line) and ("portico" in line.lower() or "AndroidRuntime" in line
            or "ReactNative" in line or "libc" in line)]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--serial", required=True)
    parser.add_argument("--apk", required=True)
    parser.add_argument("--fixture", required=True)
    parser.add_argument("--out", required=True)
    parser.add_argument("--home-timeout", type=int, default=120)
    args = parser.parse_args()
    os.makedirs(args.out, exist_ok=True)
    with open(args.fixture) as handle:
        fixture = json.load(handle)
    device = Device(args.serial, args.out)
    steps = []

    def step(name, fn):
        started = time.time()
        try:
            detail = fn()
            steps.append({"step": name, "ok": True, "seconds": round(time.time() - started, 1), "detail": detail})
        except Exception as error:  # noqa: BLE001 - every failure becomes evidence, then the run fails.
            steps.append({"step": name, "ok": False, "seconds": round(time.time() - started, 1),
                          "error": str(error).replace(fixture["password"], "<redacted>"),
                          "visible": device.text()[:400].replace(fixture["password"], "<redacted>"),
                          "screenshot": device.screenshot(f"{name}-failed")})
            raise

    def install():
        output, code, error = device.adb("install", "-r", "-d", args.apk)
        assert code == 0 and "Success" in output, f"adb install failed: {output or error}"
        # Stop first, then clear, then wait for the platform to finish: clear's own kill is
        # asynchronous and once landed 120 ms into the next launch.
        device.quiesce()
        output, code, error = device.adb("shell", "pm", "clear", PACKAGE)
        assert code == 0 and "Success" in output, f"pm clear failed: {output or error}"
        device.idle()
        # The password field is masked; also stop Android flashing the last typed character, so no
        # screenshot or dump taken while signing in can carry any of it.
        device.adb("shell", "settings", "put", "system", "show_password", "0")
        device.adb("logcat", "-c")
        return "installed and cleared"

    def launch(name):
        device.idle()
        device.adb("shell", "am", "start", "-W", "-n", f"{PACKAGE}/.MainActivity")
        time.sleep(5)
        if not device.alive() and not fatal_lines(device):
            # A known platform race (a stop still landing on the launch) kills the process with no
            # fatal. Retry once; a fatal is never retried, and the whole logcat is still scanned.
            print(f"release-smoke: {name}: the app process vanished during launch with no fatal in logcat; "
                  "treating it as the platform stop race and retrying the launch once", file=sys.stderr)
            device.quiesce()
            device.adb("shell", "am", "start", "-W", "-n", f"{PACKAGE}/.MainActivity")
            time.sleep(5)
            assert device.alive(), "the app process is gone right after launch (also on the one retry)"
            device.screenshot(name)
            return "launched on the one retry after a process loss with no fatal"
        assert device.alive(), "the app process is gone right after launch"
        device.screenshot(name)
        return "launched"

    def sign_in():
        assert device.wait_text(r"Welcome home|Sign in with Portico Account|Use a local server", 90), "the app drew no entry screen"
        if not device.tap_where(lambda node: node["id"].endswith("accounts-welcome-local")) and not device.tap_where(
                lambda node: re.search(r"use a local server|sign in directly to a server", node["text"] + node["desc"], re.I)):
            raise AssertionError("the entry screen draws no local-server option")
        assert device.wait_text(r"Server address|address", 30), "the server screen did not open"
        fields = device.fields()
        assert fields, "the server screen drew no address field"
        device.tap(fields[0]["bounds"])
        device.type_text(fixture["emulatorUrl"])
        y = device.center(fields[0]["bounds"])[1]
        below = device.buttons_below(y)
        assert below, "no primary button below the address field"
        device.tap(below[0]["bounds"])
        assert device.wait_text(r"Username|Password", 60), "the local sign-in form did not open"
        fields = device.fields()
        assert len(fields) >= 2, f"the local form drew {len(fields)} fields"
        for index, value in ((0, fixture["username"]), (1, fixture["password"])):
            device.tap(fields[index]["bounds"])
            device.type_text(value)
        device.shell("input", "keyevent", "111")  # Escape: close the keyboard so the button is in view.
        password_y = device.center(device.fields()[1]["bounds"])[1] if len(device.fields()) >= 2 else 0
        submit = next((node for node in device.nodes() if re.search(r"^(sign in|continue)$", node["text"], re.I)), None)
        target = submit or (device.buttons_below(password_y) or [None])[0]
        assert target, "no sign-in button below the password field"
        device.tap(target["bounds"])
        text = device.wait_text(r"Who's watching|Main profile|" + HOME.pattern, 90)
        assert text, "sign-in did not reach the app"
        if re.search(r"Who's watching|Main profile", text, re.I) and not HOME.search(text):
            device.tap_where(lambda node: node["clickable"] and re.search(re.escape(fixture["username"]) + r"|main profile",
                                                                          node["text"] + node["desc"], re.I))
        return "signed in"

    def home(name):
        def check():
            text = device.wait_text(HOME.pattern, args.home_timeout)
            assert text, "Home did not draw"
            assert device.alive(), "the app process died on Home"
            device.screenshot(name)
            return "Home drew"
        return check

    def restart():
        device.quiesce()
        return launch("signed-in-start")

    failed = None
    try:
        step("install", install)
        step("launch", lambda: launch("cold-launch"))
        step("sign-in", sign_in)
        step("home", home("home"))
        step("signed-in-start", restart)
        step("home-again", home("home-again"))
    except Exception as error:  # noqa: BLE001
        failed = str(error).replace(fixture["password"], "<redacted>")
    fatals = fatal_lines(device)
    log, _, _ = device.adb("logcat", "-d", "-b", "main,system,crash", timeout=120)
    with open(os.path.join(args.out, "logcat.txt"), "w") as handle:
        handle.write(log.replace(fixture["password"], "<redacted>"))
    if fatals and not failed:
        failed = f"{len(fatals)} fatal line(s) in logcat"
    summary = {"ok": failed is None, "failed": failed, "fatal": fatals[:20], "steps": steps}
    with open(os.path.join(args.out, "report.json"), "w") as handle:
        json.dump(summary, handle, indent=2)
    print(json.dumps(summary))
    return 0 if failed is None else 1


if __name__ == "__main__":
    sys.exit(main())
