#!/usr/bin/env python3
"""Run the explicit standalone Android inventory and preserve every suite's output.

The checked-in harnesses own their compiler/classpath setup. Missing prerequisites,
compile errors and timeouts are failures; they never silently reduce the inventory.
"""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

from platform_test_process import run_bounded

ROOT = Path(__file__).resolve().parents[1]
HARNESSES = (
    "apps/native/scripts/test-reading-stream-android.py",
    "apps/native/scripts/test-layout-observation-android.py",
    "apps/native/scripts/test-url-routes-android.py",
)


def run(root, output):
    output.mkdir(parents=True, exist_ok=True)
    results = []
    for source in HARNESSES:
        log = output / (Path(source).stem + ".log")
        # Each harness compiles into its own required scratch folder.
        scratch = output / (Path(source).stem + "-scratch")
        scratch.mkdir(parents=True, exist_ok=True)
        with log.open("w") as stream:
            try:
                status = run_bounded([sys.executable, str(root / source), "--scratch", str(scratch)], root, stream, 600)
            except subprocess.TimeoutExpired:
                stream.write("\nFAIL: standalone Android harness exceeded its 10-minute bound.\n")
                status = 124
            except OSError as error:
                stream.write(f"\nFAIL: {error}\n")
                status = 1
        results.append({"source": source, "exit": status, "log": log.name})
        (output / "results.json").write_text(json.dumps(results, indent=2) + "\n")
        print(f"{'PASS' if status == 0 else 'FAIL'} {source}: {log}", flush=True)
    return int(any(result["exit"] for result in results))


if __name__ == "__main__":
    directory = Path(os.environ.get("PORTICO_ANDROID_TEST_OUT_DIR") or
                     tempfile.mkdtemp(prefix="portico-android-tests-"))
    raise SystemExit(run(ROOT, directory))
