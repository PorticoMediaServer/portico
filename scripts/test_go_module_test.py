import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("test-go-module.sh")
PACKAGES = ["internal/catalog", "internal/catalog/scalebench", "internal/httpapi"]

FAKE_GO = '''#!/usr/bin/env python3
import json, os, sys

args = sys.argv[1:]
# Toolchain diagnostics are independent of the test-execution inventory below.
if args == ["version"]:
    print("go version go1.26.10 test-fixture")
    raise SystemExit(0)
with open(os.environ["CALLS"], "a", encoding="utf-8") as log:
    log.write(json.dumps(args) + "\\n")
if args[0] == "list":
    for name in filter(None, os.environ["PACKAGES"].split(",")):
        print("example.com/m/" + name + "\\t" + os.path.join(os.getcwd(), name))
    raise SystemExit(0)
if args[0] == "vet":
    raise SystemExit(0)
assert args[0] == "test", args
package = args[-1].removeprefix("./")
if package in filter(None, os.environ.get("FAILING", "").split(",")):
    raise SystemExit(int(os.environ["FAILING_EXIT"]))
print(json.dumps({"Action": "pass", "Elapsed": 0.01}), flush=True)
raise SystemExit(0)
'''


class ModuleTestInventory(unittest.TestCase):
    """The nightly phase runs every package alone instead of one monolithic call."""

    def run_script(self, module, packages=PACKAGES, failing=(), failing_exit=3):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            fake = root / "go"
            fake.write_text(FAKE_GO, encoding="utf-8")
            fake.chmod(0o755)
            calls = root / "calls"
            environment = {**os.environ,
                           "PATH": str(root) + os.pathsep + os.environ["PATH"],
                           "CALLS": str(calls), "PACKAGES": ",".join(packages),
                           "FAILING": ",".join(failing), "FAILING_EXIT": str(failing_exit)}
            result = subprocess.run(["sh", str(SCRIPT), module], env=environment,
                                    capture_output=True, text=True, timeout=120)
            logged = ([json.loads(line) for line in calls.read_text().splitlines()]
                      if calls.exists() else [])
            return result, logged

    def test_every_package_runs_alone_with_json_and_its_own_timeout(self):
        result, calls = self.run_script("server")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(calls[0][0], "list")
        tests = calls[1:]
        # Lanes start concurrently, so only the covered set is deterministic.
        self.assertEqual(sorted(call[-1] for call in tests),
                         ["./internal/catalog", "./internal/catalog/scalebench", "./internal/httpapi"])
        for call in tests:
            self.assertEqual(call[:4], ["test", "-json", "-count=1", "-timeout=780s"])
            # Never a monolithic phase, and never a second mechanism for vet.
            self.assertNotIn("./...", call)
        self.assertNotIn("vet", [call[0] for call in calls])

    def test_a_failing_package_fails_the_gate_and_the_rest_still_runs(self):
        result, calls = self.run_script("server", failing=["internal/httpapi"], failing_exit=7)
        # The phase is a gate: any package failure stops it, named, with the rest run.
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertEqual(sorted(call[-1] for call in calls[1:]),
                         ["./internal/catalog", "./internal/catalog/scalebench", "./internal/httpapi"])
        self.assertIn("FAIL  ./internal/httpapi", result.stdout)

    def test_apikit_uses_the_same_complete_inventory(self):
        result, calls = self.run_script("apikit")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(sorted(call[-1] for call in calls[1:]),
                         sorted("./" + name for name in PACKAGES))

    def test_an_unknown_module_is_rejected_without_running_go(self):
        result, calls = self.run_script("web", packages=[])
        self.assertEqual(result.returncode, 2)
        self.assertIn("expected module: apikit or server", result.stderr)
        self.assertEqual(calls, [])


if __name__ == "__main__":
    unittest.main()
