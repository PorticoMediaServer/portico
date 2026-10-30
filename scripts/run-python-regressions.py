#!/usr/bin/env python3
"""Run the explicit offline Python regression inventory and retain receipts."""
import argparse
import importlib.util
import json
import os
import pathlib
import signal
import subprocess
import sys
import time
import unittest

ROOT = pathlib.Path(__file__).resolve().parent.parent
MANIFEST = ROOT / "scripts/python-regressions.json"


def emit(stream, **event):
    stream.write(json.dumps(event) + "\n")
    stream.flush()


def worker(path, events, test_timeout):
    specification = importlib.util.spec_from_file_location("regression_suite", path)
    module = importlib.util.module_from_spec(specification)
    sys.modules[specification.name] = module
    specification.loader.exec_module(module)
    suite = unittest.defaultTestLoader.loadTestsFromModule(module)
    if suite.countTestCases() == 0:
        raise RuntimeError("Regression suite discovered no tests: " + str(path))
    with events.open("w") as receipts:
        class Result(unittest.TextTestResult):
            def startTest(self, test):
                super().startTest(test)
                self.started = time.monotonic()
                emit(receipts, test=test.id(), status="started")
                self.previous_alarm = signal.signal(signal.SIGALRM, self.expired)
                signal.setitimer(signal.ITIMER_REAL, test_timeout)

            def expired(self, _signal, _frame):
                raise TimeoutError(f"test exceeded its {test_timeout}s deadline")

            def stopTest(self, test):
                signal.setitimer(signal.ITIMER_REAL, 0)
                signal.signal(signal.SIGALRM, self.previous_alarm)
                super().stopTest(test)

            def receipt(self, test, status, **extra):
                emit(receipts, test=test.id(), status=status,
                     seconds=round(time.monotonic() - getattr(self, "started", time.monotonic()), 3), **extra)

            def addSuccess(self, test):
                self.receipt(test, "passed")
                super().addSuccess(test)

            def addFailure(self, test, error):
                self.receipt(test, "failed", detail=self._exc_info_to_string(error, test))
                super().addFailure(test, error)

            def addError(self, test, error):
                self.receipt(test, "error", detail=self._exc_info_to_string(error, test))
                super().addError(test, error)

            def addSkip(self, test, reason):
                self.receipt(test, "skipped", reason=reason)
                super().addSkip(test, reason)

            def addSubTest(self, test, subtest, error):
                detail = {"detail": self._exc_info_to_string(error, test)} if error else {}
                self.receipt(subtest, "failed" if error else "passed", **detail)
                super().addSubTest(test, subtest, error)

            def addExpectedFailure(self, test, error):
                self.receipt(test, "expected_failure", detail=self._exc_info_to_string(error, test))
                super().addExpectedFailure(test, error)

            def addUnexpectedSuccess(self, test):
                self.receipt(test, "unexpected_success")
                super().addUnexpectedSuccess(test)

        result = unittest.TextTestRunner(verbosity=2, resultclass=Result).run(suite)
    return 0 if result.wasSuccessful() else 1


def stop_tree(process):
    # Every suite owns a process group, including helpers a failed test started.
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        return
    try:
        process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        pass
    try:
        os.killpg(process.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    process.wait()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--report-dir", type=pathlib.Path, default=ROOT / ".scratch/python-regressions")
    parser.add_argument("--suite", action="append", help="Run only this explicitly inventoried suite (repeatable)")
    parser.add_argument("--list", action="store_true")
    parser.add_argument("--worker", type=pathlib.Path, help=argparse.SUPPRESS)
    parser.add_argument("--events", type=pathlib.Path, help=argparse.SUPPRESS)
    parser.add_argument("--test-timeout", type=int, help=argparse.SUPPRESS)
    args = parser.parse_args()
    if args.worker:
        return worker(args.worker, args.events, args.test_timeout)
    manifest = json.loads(MANIFEST.read_text())
    suites = manifest["suites"]
    known = {suite["path"] for suite in suites}
    if manifest["schema"] != 1 or len(known) != len(suites):
        parser.error("invalid or duplicate Python regression inventory")
    if args.suite and not set(args.suite) <= known:
        parser.error("--suite must name an entry in scripts/python-regressions.json")
    if args.suite:
        suites = [suite for suite in suites if suite["path"] in args.suite]
    if args.list:
        print("\n".join(suite["path"] for suite in suites))
        return 0
    args.report_dir.mkdir(parents=True, exist_ok=True)
    results = []
    for suite in suites:
        name = pathlib.Path(suite["path"]).stem
        log = args.report_dir / (name + ".log")
        events = args.report_dir / (name + ".jsonl")
        command = [sys.executable, "-u", str(pathlib.Path(__file__).resolve()),
                   "--worker", str(ROOT / suite["path"]), "--events", str(events),
                   "--test-timeout", str(suite["testTimeoutSeconds"])]
        started = time.monotonic()
        with log.open("w") as output:
            process = subprocess.Popen(command, cwd=ROOT, stdout=output,
                                       stderr=subprocess.STDOUT, start_new_session=True)
            try:
                code = process.wait(timeout=suite["timeoutSeconds"])
                status = "passed" if code == 0 else "failed"
            except subprocess.TimeoutExpired:
                stop_tree(process)
                code, status = process.returncode, "timed_out"
                output.write(f"\nSuite exceeded its {suite['timeoutSeconds']}s deadline.\n")
            finally:
                # A finished test process must not leave its fixture children.
                stop_tree(process)
        result = dict(suite=suite["path"], status=status, exitCode=code,
                      seconds=round(time.monotonic() - started, 3))
        results.append(result)
        (args.report_dir / "results.json").write_text(json.dumps(results, indent=2) + "\n")
        print(f"{suite['path']}: {status}", flush=True)
        print("\n".join(log.read_text().splitlines()[-12:]), flush=True)
    return int(any(result["status"] != "passed" for result in results))


if __name__ == "__main__":
    raise SystemExit(main())
