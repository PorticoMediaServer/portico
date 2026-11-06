"""Report-only native plan collection; never updates reviewed source/goldens."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import subprocess
import sys

STORE = Path("server/internal/store")
TARGETS = {
    "stmt/plan": ("plan_test", ["canonical_test.go", "autoindexes_test.go",
                              "autoindex_fences_test.go", "autoindex_keysets_test.go"],
                  ["TestPlanCanonicalSchemaFKAndStatementContracts",
                   "TestPlanAutoindexGoldenAndNativeKeys", "TestPlanAutoindexRealJoinedFences",
                   "TestPlanAutoindexPolicyChildKeysetGolden", "TestPlanAutoindexInclusiveChannelKeysetsGolden"]),
    "writer": ("writer", ["scan_plan_test.go", "scan_object_plan_test.go",
                          "scan_fresh_plan_test.go", "search_input_plan_test.go",
                          "inventory_disclosure_plan_test.go", "effective_dispatch_test.go",
                          "search_image_stage_plan_test.go"],
               ["TestScanInventoryStatementPlans", "TestScanObjectStatementPlans",
                "TestScanFreshStatementPlans", "TestSearchInputStatementPlans",
                "TestInventoryDisclosurePlanGoldens", "TestInventoryFreshLibraryPlanGolden",
                "TestSearchImageBeginStatementPlans"]),
    "read": ("read", ["plans_test.go", "foreground_plans_test.go"],
             ["TestBrowsePlans", "TestForegroundPlans"]),
}


def digest(data):
    return hashlib.sha256(data).hexdigest()


def git(source, *args):
    return subprocess.check_output(["git", "-C", str(source), *args], text=True).strip()


def check_locations(source, output, sha):
    if not re.fullmatch(r"[0-9a-f]{40}", sha):
        raise ValueError("source SHA must be an exact 40-character commit")
    if output == source or source in output.parents:
        raise ValueError("diagnostics must be outside the source checkout")
    if git(source, "rev-parse", "HEAD") != sha:
        raise ValueError("checked-out source differs from requested SHA")
    if git(source, "status", "--porcelain"):
        raise ValueError("source checkout must be clean")


def overlay(source, output, template):
    """Only test compilation sees the wrapper; os.ReadFile journals stay exact."""
    replacements, changes = {}, []
    for package, (name, files, _) in TARGETS.items():
        directory = source / STORE / package
        generated = output / "overlay" / package
        generated.mkdir(parents=True, exist_ok=True)
        helper = generated / "reporter_test.go"
        helper.write_text(template.replace("package PACKAGE", "package " + name, 1))
        virtual = directory / "zz_native_pair_reporter_test.go"
        if virtual.exists():
            raise ValueError("diagnostic test name already exists")
        replacements[str(virtual)] = str(helper)
        for filename in files:
            original = directory / filename
            data = original.read_bytes()
            text = data.decode()
            changed = text.replace("plan.Capture(", "nativePairCapture(t, ")
            changed = changed.replace("plan.ForeignKeys(", "nativePairForeignKeys(t, ")
            if filename == "canonical_test.go":
                # Keep this assertion, but collect the complete VM before its
                # environment/FK mismatch can terminate the canonical test.
                old = "\tcheckFKGolden(t, fk)\n"
                if text.count(old) != 1:
                    raise ValueError("canonical FK check changed; inspect before dispatch")
                changed = changed.replace(old, "\tt.Cleanup(func() { checkFKGolden(t, fk) })\n", 1)
            if changed == text:
                raise ValueError("missing expected Capture call: " + str(original))
            replacement = generated / filename
            replacement.write_text(changed)
            replacements[str(original)] = str(replacement)
            changes.append({"path": str(original.relative_to(source)),
                            "sourceSHA256": digest(data),
                            "overlaySHA256": digest(changed.encode())})
    path = output / "overlay.json"
    path.write_text(json.dumps({"Replace": replacements}, indent=2) + "\n")
    return path, changes


def collect(source, output, sha):
    check_locations(source, output, sha)
    output.mkdir(parents=True, exist_ok=True)
    template = Path(__file__).with_name("native_plan_pair_test.go.tmpl").read_text()
    overlay_path, changes = overlay(source, output, template)
    baseline = []
    for path in git(source, "ls-files", str(STORE)).splitlines():
        p = Path(path)
        if "testdata" not in p.parts or not (path.endswith(".json") or path.endswith(".json.gz")):
            continue
        if not any(p.is_relative_to(STORE / package / "testdata") for package in TARGETS):
            continue
        data = (source / p).read_bytes()
        target = output / "baseline" / p
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)
        baseline.append({"path": path, "SHA256": digest(data), "bytes": len(data)})
    tags = subprocess.check_output([sys.executable, str(source / "scripts/release-build-tags.py"), "--capability"], text=True).strip()
    manifest = {"sourceSHA": sha, "sourceTree": git(source, "rev-parse", "HEAD^{tree}"),
                "toolSHA": git(Path(__file__).resolve().parents[1], "rev-parse", "HEAD"),
                "platform": platform.platform(), "machine": platform.machine(),
                "goVersion": subprocess.check_output(["go", "version"], text=True).strip(),
                "tags": tags, "templateSHA256": digest(template.encode()),
                "sourceOverlay": changes, "baseline": baseline,
                "recordFlags": False, "runs": [], "state": "running"}
    report = output / "manifest.json"
    report.write_text(json.dumps(manifest, indent=2) + "\n")
    env = os.environ.copy()
    scratch = output / "tmp"
    scratch.mkdir(exist_ok=True)
    env.update(GOMAXPROCS="2", TMPDIR=str(scratch.resolve()))
    try:
        for package, (_, _, tests) in TARGETS.items():
            native = output / "actual" / package
            env["PORTICO_NATIVE_PLAN_OUTPUT"] = str(native)
            command = ["go", "-C", str(source / "server"), "test", "-overlay", str(overlay_path),
                       "-mod=readonly", "-tags", tags, "-p", "1", "-parallel", "2", "-count=1",
                       "-timeout=5m", "-run", "^(" + "|".join(tests) + ")$",
                       "./internal/store/" + package, "-v"]
            log = output / (package.replace("/", "-") + ".log")
            with log.open("w") as stream:
                result = subprocess.run(command, env=env, stdout=stream, stderr=subprocess.STDOUT)
            manifest["runs"].append({"package": package, "command": command,
                                     "exitCode": result.returncode, "log": log.name,
                                     "logSHA256": digest(log.read_bytes())})
            print(package, "exit", result.returncode, flush=True)
        if git(source, "status", "--porcelain"):
            raise ValueError("diagnostic altered reviewed source")
        for item in baseline:
            if digest((source / item["path"]).read_bytes()) != item["SHA256"]:
                raise ValueError("diagnostic altered baseline: " + item["path"])
        manifest["state"] = "complete"
    finally:
        manifest["artifacts"] = [{"path": str(p.relative_to(output)), "bytes": p.stat().st_size,
                                  "SHA256": digest(p.read_bytes())}
                                 for p in sorted((output / "actual").rglob("*.json.gz"))]
        report.write_text(json.dumps(manifest, indent=2) + "\n")
        shutil.rmtree(scratch)
    # Preserve normal failure status; macOS fingerprint failures are expected.
    return int(any(run["exitCode"] != 0 for run in manifest["runs"]))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--sha", required=True)
    args = parser.parse_args()
    raise SystemExit(collect(args.source.resolve(), args.output.resolve(), args.sha))
