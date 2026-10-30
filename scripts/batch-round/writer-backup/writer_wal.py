"""Bounded real source-inventory/quiet-gap observation for P2-65.

This is an observation tool, not the agreed 300k real-scan scale proof (which
replaces the earlier 5M target). It triggers one real
library scan, samples the real owner diagnostics (WAL/holds/plans/pressure) and
reports the quiet-gap cases it can and cannot see.
"""

from __future__ import annotations

import re
import time
import urllib.parse
from dataclasses import dataclass
from pathlib import Path

from common import DIAGNOSTICS_PATH, ApiError, Refused, Sampler, check_summary, clean_pass, evaluate_thresholds, now_iso, overall

TERMINAL = {"complete", "complete_with_warnings", "failed", "cancelled"}
QUIET_GAP = re.compile(r"Reading COMPLETE deferred:.*waiting for .* without changes")
FOLDER = re.compile(r'folder="[^"]*"')
LOG_TAIL_BYTES = 8 * 1024 * 1024


@dataclass(frozen=True)
class WriterOptions:
    library_id: str
    mode: str = "incremental"
    observe_seconds: float = 120.0
    poll_seconds: float = 1.0
    stdout_log: str | None = None
    out: str = ""


def scan_path(library_id: str, mode: str) -> str:
    return f"/v1/libraries/{urllib.parse.quote(library_id, safe='')}/scans?mode={urllib.parse.quote(mode)}"


def _search_log(path: str) -> dict:
    p = Path(path)
    if not p.is_file():
        return {"available": True, "read": False, "error": "log file is absent"}
    try:
        with p.open("rb") as handle:
            handle.seek(0, 2)
            size = handle.tell()
            handle.seek(max(0, size - LOG_TAIL_BYTES))
            text = handle.read().decode("utf-8", "replace")
    except OSError as error:
        return {"available": True, "read": False, "error": type(error).__name__}
    lines = []
    for line in text.splitlines():
        if QUIET_GAP.search(line):
            lines.append(FOLDER.sub('folder="<redacted>"', line.strip())[:512])
            if len(lines) >= 20:
                break
    return {
        "available": True,
        "read": True,
        "tailBytes": min(size, LOG_TAIL_BYTES),
        "matches": len(lines),
        "lines": lines,
        "note": "quiet-gap publication has no published diagnostics surface; this is a process-log observation only",
    }


def run(api, options: WriterOptions, *, clock=time.monotonic, sleep=time.sleep) -> dict:
    if not options.library_id:
        raise Refused("a real library id is required")
    if options.mode not in ("incremental", "full", "integrity"):
        raise Refused("scan mode must be incremental, full or integrity")
    sampler = Sampler(lambda: api.request("GET", DIAGNOSTICS_PATH), clock=clock)
    sampler.sample()
    receipt: dict = {
        "kind": "p2-65-writer-wal-falsifier",
        "version": 1,
        "startedAt": now_iso(),
        "libraryId": options.library_id,
        "mode": options.mode,
        "observeSeconds": options.observe_seconds,
    }
    checks: list[dict] = []
    failure = None
    try:
        accepted = api.request("POST", scan_path(options.library_id, options.mode))
        if not isinstance(accepted, dict) or not isinstance(accepted.get("id"), str):
            raise Refused("scan start did not answer a real ingestion job")
        job_id = accepted["id"]
        receipt["job"] = {
            "id": job_id,
            "status": accepted.get("status"),
            "scanMode": accepted.get("scanMode"),
            "sourceId": accepted.get("sourceId"),
        }
        samples = []
        deadline = clock() + options.observe_seconds
        terminal = None
        while clock() < deadline:
            job = api.request("GET", f"/v1/admin/ingestion/jobs/{urllib.parse.quote(job_id, safe='')}")
            sampler.sample()
            if not isinstance(job, dict):
                raise Refused("ingestion job document is not an object")
            status = job.get("status")
            samples.append(
                {
                    "status": status,
                    "phase": job.get("phase"),
                    "processed": job.get("processed"),
                    "analyzed": job.get("analyzed"),
                    "warnings": job.get("warnings"),
                }
            )
            if status in TERMINAL:
                terminal = status
                break
            sleep(options.poll_seconds)
        receipt["job"]["samples"] = _collapse(samples)
        receipt["job"]["terminal"] = terminal
        receipt["job"]["processedLast"] = samples[-1]["processed"] if samples else None
        if terminal is None:
            checks.append(
                {
                    "id": "scan.terminal",
                    "status": "finding",
                    "detail": "the observation window ended while the scan was still running; this is bounded observation, not a completion proof",
                }
            )
        elif terminal == "failed":
            checks.append({"id": "scan.terminal", "status": "fail", "detail": "the real scan failed"})
            failure = "scan_failed"
        elif terminal == "cancelled":
            checks.append({"id": "scan.terminal", "status": "finding", "detail": "the scan was cancelled; this is not a completion proof"})
        else:
            checks.append({"id": "scan.terminal", "status": "pass", "detail": f"scan ended {terminal}"})
    except ApiError as error:
        failure = "api_error"
        checks.append(
            {
                "id": "api.request",
                "status": "fail",
                "detail": f"owner API call failed with HTTP {error.status} {error.code}",
            }
        )
    except Refused as error:
        failure = "refused"
        checks.append({"id": "precondition", "status": "fail", "detail": str(error)})
    finally:
        summary = sampler.summary()
        receipt["diagnostics"] = summary
        checks.extend(evaluate_thresholds(summary, context="source-inventory"))
        receipt["quietGap"] = (
            _search_log(options.stdout_log)
            if options.stdout_log
            else {
                "available": False,
                "reason": "no --stdout-log supplied; quiet-gap publication has no published diagnostics surface",
            }
        )
        receipt["unsupported"] = [
            "agreed 300k real-scan scale bar (replaces the earlier 5M target): separate scale stage; this 100-file observation is not that bar",
            "bounded removal/retention and SRV023 10k-track release: owned by P2-14a/P2-47/P2-52b/P2-54",
            "user-write progress during high-fanout inventory: needs the mixed-client load driver, not this observer",
            "before-BEGIN WAL debits/overruns/unknown I/O: internal dbwork.WALReservations, not on the diagnostics wire",
            "quiet-gap deadline/state: internal reading_path_inputs; only a process-log line can be observed here",
            "artwork collector queue: no per-collector diagnostic; GC/heap and storage-helper stats are sampled",
        ]
        receipt["thresholds"] = checks
        receipt["checkSummary"] = check_summary(checks)
        receipt["cleanPass"] = clean_pass(checks)
        receipt["claim"] = "clean-pass" if receipt["cleanPass"] else "gate-pass-with-findings-or-unavailable"
        receipt["finishedAt"] = now_iso()
        if failure:
            receipt["result"] = "fail"
        else:
            receipt["result"] = overall(checks)
    return receipt


def _collapse(samples: list[dict]) -> list[dict]:
    """Keep status changes and the last sample; bounded receipt size."""
    out: list[dict] = []
    previous = None
    for sample in samples:
        key = (sample.get("status"), sample.get("phase"))
        if key != previous:
            out.append(sample)
            previous = key
    if samples and (not out or out[-1] != samples[-1]):
        out.append(samples[-1])
    return out[-40:]


# ------------------------------------------------------------------ self-check
class _FakeClock:
    def __init__(self):
        self.value = 0.0

    def __call__(self) -> float:
        return self.value

    def sleep(self, seconds: float) -> None:
        self.value += max(0.0, float(seconds))


class _FakeApi:
    def __init__(self, handler):
        self.handler = handler
        self.calls = []

    def request(self, method, path, body=None):
        self.calls.append((method, path))
        return self.handler(method, path, body)


def _diagnostics(peak: int = 2048) -> dict:
    return {
        "wal": {
            "walFileBytes": peak,
            "walPeakFileBytes": peak,
            "logFrames": 12,
            "checkpointedFrames": 12,
            "backlogFrames": 0,
            "busy": 0,
            "readers": {"snapshots": 2, "admissionRefused": 0},
            "checkpoints": {"attempts": 4, "truncates": 1, "busy": 0, "errors": 0, "pressureTimeouts": 0, "maxDurationMs": 12},
        },
        "writeGate": {
            "active": False,
            "heldMillis": 500,
            "maxHeldMillis": 120,
            "queueWaitMillis": 20,
            "byClass": {"background-media": {"currentHeldMillis": 0, "maxHeldMillis": 120, "queued": 0}},
        },
        "writerAdmission": {
            "capacity": 8,
            "userQueued": 0,
            "backgroundQueued": 1,
            "deferred": 0,
            "refused": 0,
            "busyAfterAdmission": 0,
            "recoveryHold": False,
            "recoveryPause": False,
        },
        "writePlans": {"measured": 30, "overruns": 0, "overrunsByClass": {}, "overrunsByDimension": {}, "largest": {}},
        "pressure": {"yields": 3, "foregroundActive": True, "healthy": True},
        "health": {"state": "healthy", "consecutiveFailures": 0, "lastError": "", "handleRecycles": 0, "backgroundPaused": False},
        "process": {"goroutines": 30, "heapAllocBytes": 2048, "nextGCBytes": 4096, "gcCycles": 4, "containedPanics": 0},
        "publications": 9,
        "ungatedWrites": {"writer": 0, "reader": 0},
    }


def self_check() -> dict:
    results = {}

    def scenario_complete():
        clock = _FakeClock()
        state = {"polls": 0}

        def handler(method, path, body):
            if method == "POST" and "/scans?" in path:
                return {"id": "job-1", "status": "queued", "scanMode": "incremental"}
            if method == "GET" and path.startswith("/v1/admin/ingestion/jobs/"):
                state["polls"] += 1
                if state["polls"] < 3:
                    return {"id": "job-1", "status": "running", "phase": "inventory", "processed": state["polls"]}
                return {"id": "job-1", "status": "complete", "phase": "complete", "processed": 3}
            if method == "GET" and path == DIAGNOSTICS_PATH:
                return _diagnostics()
            raise AssertionError((method, path))

        api = _FakeApi(handler)
        receipt = run(api, WriterOptions("lib-1", observe_seconds=30), clock=clock, sleep=clock.sleep)
        assert receipt["result"] == "pass", receipt
        assert receipt["job"]["terminal"] == "complete"
        assert receipt["quietGap"]["available"] is False
        return receipt["result"]

    def scenario_failed():
        clock = _FakeClock()

        def handler(method, path, body):
            if method == "POST" and "/scans?" in path:
                return {"id": "job-2", "status": "queued"}
            if method == "GET" and path.startswith("/v1/admin/ingestion/jobs/"):
                return {"id": "job-2", "status": "failed"}
            if method == "GET" and path == DIAGNOSTICS_PATH:
                return _diagnostics(peak=200 * 1024 * 1024)
            raise AssertionError((method, path))

        api = _FakeApi(handler)
        receipt = run(api, WriterOptions("lib-1", observe_seconds=5), clock=clock, sleep=clock.sleep)
        assert receipt["result"] == "fail", receipt
        assert any(item["id"] == "wal.physicalPeakWithin192MiB" and item["status"] == "fail" for item in receipt["thresholds"])
        return receipt["result"]

    results["inventory-complete"] = scenario_complete()
    results["inventory-failed"] = scenario_failed()
    return results
