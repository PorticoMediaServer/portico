"""Shared bounded helpers for the P2-65 backup/writer operator tools.

These are operator tools, not CI entry points. They never modify product code or
product data beyond the explicitly named API operation, and they refuse to run
in CI. Every write is an owner-API call whose inverse is recorded first.
"""

from __future__ import annotations

import hashlib
import json
import os
import re
import stat
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

# The one hard physical WAL bound this pin publishes (dbwork.CheckpointHardFileBytes).
WAL_PHYSICAL_LIMIT_BYTES = 192 * 1024 * 1024
# A sampled writer hold at or above this is a redesign finding, not a pass.
HOLD_FINDING_MILLIS = 250
# Scheduled backups use the approved 60 s no-progress budget (P2-13). The only
# tolerance is the measured sampler interval, never a wider product gate.
SCHEDULED_NO_PROGRESS_BUDGET_MILLIS = 60_000
SCHEDULED_SAMPLER_TOLERANCE_MAX_MILLIS = 12_000


def scheduled_no_progress_tolerance_ms(poll_seconds: float) -> int:
    """One sampler interval plus a small scheduling margin; never a policy widening."""
    return min(SCHEDULED_SAMPLER_TOLERANCE_MAX_MILLIS, int(max(0.0, poll_seconds) * 1000) + 2_000)


BACKUPS_PATH = "/v1/admin/backups"
MAINTENANCE_PATH = "/v1/admin/maintenance/settings"
DIAGNOSTICS_PATH = "/v1/admin/diagnostics/concurrency"
READINESS_PATH = "/v1/readiness"

RECEIPT_MAX_BYTES = 8 * 1024 * 1024
TOKEN_MAX_BYTES = 4096


class Refused(Exception):
    """A precondition the operator tool will not cross."""


class ApiError(Exception):
    def __init__(self, status: int, code: str, detail: str = ""):
        self.status = status
        self.code = code
        self.detail = detail
        super().__init__(f"HTTP {status} {code}")


def refuse_in_ci(execute: bool) -> None:
    # Presence is sufficient, including CI=false. There is no override.
    if execute and ("CI" in os.environ or "GITHUB_ACTIONS" in os.environ):
        raise Refused("operator execution is refused in CI")


def clean_absolute(value: str | os.PathLike[str], *, must_exist: bool = False) -> Path:
    path = Path(value)
    if not path.is_absolute():
        raise Refused(f"path must be absolute: {path}")
    for component in (path, *path.parents):
        if component.is_symlink():
            raise Refused(f"symlinked operator path refused: {component}")
    if must_exist and not path.exists():
        raise Refused(f"path does not exist: {path}")
    return path


def read_token_file(value: str | os.PathLike[str]) -> str:
    path = clean_absolute(value, must_exist=True)
    info = path.stat()
    if not stat.S_ISREG(info.st_mode) or info.st_size > TOKEN_MAX_BYTES:
        raise Refused("token file must be a small regular file")
    if info.st_uid != os.getuid():
        raise Refused("token file must be owned by the operator")
    if info.st_mode & 0o077:
        raise Refused("token file must be mode 0600")
    token = path.read_text(encoding="utf-8").strip()
    if not token or "\n" in token or len(token) > TOKEN_MAX_BYTES:
        raise Refused("token file must contain one bearer token")
    return token


def read_env_file(value: str | os.PathLike[str]) -> dict[str, str]:
    """Read KEY=VALUE lines for a scratch server. Never logs values."""
    path = clean_absolute(value, must_exist=True)
    info = path.stat()
    if not stat.S_ISREG(info.st_mode) or info.st_size > 64 * 1024:
        raise Refused("env file must be a small regular file")
    if info.st_uid != os.getuid() or info.st_mode & 0o077:
        raise Refused("env file must be operator-owned mode 0600")
    out: dict[str, str] = {}
    for number, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        if not line or line.startswith("#"):
            continue
        if "=" not in line:
            raise Refused(f"env line {number} is not KEY=VALUE")
        key, raw = line.split("=", 1)
        if not re.fullmatch(r"[A-Z_][A-Z0-9_]{0,127}", key):
            raise Refused(f"env line {number} has an invalid key")
        out[key] = raw
    return out


def write_private_json(path: str | os.PathLike[str], value: dict) -> Path:
    """Create a new operator receipt, mode 0600, never overwriting evidence."""
    out = clean_absolute(path)
    parent = out.parent
    if not parent.is_dir() or parent.is_symlink():
        raise Refused(f"receipt parent must be an existing real directory: {parent}")
    raw = json.dumps(value, indent=2, sort_keys=True) + "\n"
    data = raw.encode("utf-8")
    if len(data) > RECEIPT_MAX_BYTES:
        raise Refused("receipt exceeds the bounded size")
    fd = os.open(out, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    try:
        os.fchmod(fd, 0o600)
        view = memoryview(data)
        while view:
            written = os.write(fd, view)
            if written <= 0:
                raise Refused("short write while creating the operator receipt")
            view = view[written:]
        os.fsync(fd)
    finally:
        os.close(fd)
    return out


def read_private_json(path: str | os.PathLike[str], *, max_bytes: int = RECEIPT_MAX_BYTES) -> dict:
    p = clean_absolute(path, must_exist=True)
    info = p.stat()
    if not stat.S_ISREG(info.st_mode) or info.st_size > max_bytes:
        raise Refused("private input must be a small regular file")
    if info.st_uid != os.getuid() or info.st_mode & 0o077:
        raise Refused("private input must be operator-owned mode 0600")
    return json.loads(p.read_text(encoding="utf-8"))


def remove_quietly(path: Path) -> None:
    try:
        path.unlink()
    except FileNotFoundError:
        pass
    except OSError as error:
        raise Refused(f"could not remove operator file: {error}") from error


def sha256_file(path: Path, *, limit_bytes: int | None = None) -> str:
    digest = hashlib.sha256()
    read = 0
    with path.open("rb") as handle:
        while True:
            chunk = handle.read(1024 * 1024)
            if not chunk:
                break
            read += len(chunk)
            if limit_bytes is not None and read > limit_bytes:
                raise Refused("hash input exceeds the bounded size")
            digest.update(chunk)
    return digest.hexdigest()


def origin_of(base: str) -> str:
    parts = urllib.parse.urlsplit(base)
    if parts.scheme not in ("http", "https") or not parts.netloc:
        raise Refused("base must be an http(s) origin")
    if parts.username or parts.password or parts.path not in ("", "/") or parts.query or parts.fragment:
        raise Refused("base must be a bare http(s) origin")
    return f"{parts.scheme}://{parts.netloc}"


class JsonApi:
    """Minimal bearer client. Bare registry documents and envelopes are both read."""

    def __init__(self, base: str, token: str, *, timeout: float = 20.0):
        self.base = origin_of(base)
        self._token = token
        self.timeout = timeout
        self.last_status: int = 0

    def request(self, method: str, path: str, body: dict | None = None):
        data = None if body is None else json.dumps(body, separators=(",", ":")).encode("utf-8")
        request = urllib.request.Request(self.base + path, data=data, method=method)
        request.add_header("Authorization", "Bearer " + self._token)
        request.add_header("Accept", "application/json")
        if data is not None:
            request.add_header("Content-Type", "application/json")
        try:
            with urllib.request.urlopen(request, timeout=self.timeout) as response:
                self.last_status = response.status
                raw = response.read(4 * 1024 * 1024)
                if response.status == 204 or not raw:
                    return None
                return unwrap(json.loads(raw))
        except urllib.error.HTTPError as error:
            raw = error.read(64 * 1024)
            code = f"http_{error.code}"
            try:
                document = json.loads(raw)
                body_error = document.get("error") if isinstance(document, dict) else None
                if isinstance(body_error, dict) and isinstance(body_error.get("code"), str):
                    code = body_error["code"]
                elif isinstance(document, dict) and isinstance(document.get("code"), str):
                    code = document["code"]
            except (ValueError, UnicodeDecodeError):
                pass
            raise ApiError(error.code, code) from None
        except (urllib.error.URLError, TimeoutError, OSError) as error:
            raise ApiError(0, "transport", str(error)) from None


def unwrap(document):
    if isinstance(document, dict) and "result" in document and "protocolVersion" in document:
        return document["result"]
    return document


def redact_restore(result: dict | None) -> dict | None:
    if not isinstance(result, dict):
        return None
    keys = ("operationId", "sourceDigest", "jobId", "schemaVersion", "at", "outcome", "reason", "recovery")
    return {key: result.get(key) for key in keys if key in result}


def redact_settings(document: dict) -> dict:
    """Maintenance settings only; no secret exists here, but keep the shape exact."""
    settings = document.get("settings")
    if not isinstance(settings, dict):
        raise Refused("maintenance document has no settings")
    return {
        "windows": settings.get("windows"),
        "retention": settings.get("retention"),
        "backupKeepCount": settings.get("backupKeepCount"),
        "backgroundSpeed": settings.get("backgroundSpeed"),
    }


DIAGNOSTIC_SECTIONS = ("wal", "writeGate", "writerAdmission", "writePlans", "pressure", "health", "process")
DIAGNOSTIC_WAL_INTEGERS = ("walFileBytes", "walPeakFileBytes", "logFrames", "checkpointedFrames", "backlogFrames", "busy")


def _require_diagnostics_shape(document: dict) -> None:
    """Fail instead of silently recording zeros when the wire shape changes."""
    for section in DIAGNOSTIC_SECTIONS:
        if not isinstance(document.get(section), dict):
            raise Refused(f"diagnostics document is missing the {section} object")
    wal = document["wal"]
    for key in DIAGNOSTIC_WAL_INTEGERS:
        if not isinstance(wal.get(key), int):
            raise Refused(f"diagnostics wal.{key} is not an integer")
    if not isinstance(wal.get("readers"), dict) or not isinstance(wal.get("checkpoints"), dict):
        raise Refused("diagnostics wal.readers/checkpoints are not objects")
    if not isinstance(document["writeGate"].get("byClass"), dict):
        raise Refused("diagnostics writeGate.byClass is not an object")


def compact_diagnostics(document: dict) -> dict:
    _require_diagnostics_shape(document)
    wal = document["wal"]
    gate = document["writeGate"]
    admission = document["writerAdmission"]
    plans = document["writePlans"]
    pressure = document["pressure"]
    health = document["health"]
    process = document["process"]
    readers = wal["readers"]
    checkpoints = wal["checkpoints"]
    by_class = gate["byClass"]
    return {
        "atUnixMs": int(time.time() * 1000),
        "wal": {
            "walFileBytes": wal.get("walFileBytes"),
            "walPeakFileBytes": wal.get("walPeakFileBytes"),
            "logFrames": wal.get("logFrames"),
            "checkpointedFrames": wal.get("checkpointedFrames"),
            "backlogFrames": wal.get("backlogFrames"),
            "busy": wal.get("busy"),
            "lastError": wal.get("lastError"),
        },
        "readers": {
            "snapshots": readers.get("snapshots"),
            "oldestSnapshotMs": readers.get("oldestSnapshotMs"),
            "maxSnapshotMs": readers.get("maxSnapshotMs"),
            "admissionWaiting": readers.get("admissionWaiting"),
            "admissionPaused": readers.get("admissionPaused"),
            "admissionRefused": readers.get("admissionRefused"),
        },
        "checkpoints": {
            "attempts": checkpoints.get("attempts"),
            "truncates": checkpoints.get("truncates"),
            "busy": checkpoints.get("busy"),
            "errors": checkpoints.get("errors"),
            "pressureTimeouts": checkpoints.get("pressureTimeouts"),
            "maxDurationMs": checkpoints.get("maxDurationMs"),
        },
        "gate": {
            "active": gate.get("active"),
            "heldMillis": gate.get("heldMillis"),
            "maxHeldMillis": gate.get("maxHeldMillis"),
            "queueWaitMillis": gate.get("queueWaitMillis"),
            "byClass": {
                name: {
                    "currentHeldMillis": value.get("currentHeldMillis"),
                    "maxHeldMillis": value.get("maxHeldMillis"),
                    "queued": value.get("queued"),
                }
                for name, value in by_class.items()
            },
        },
        "admission": {
            "capacity": admission.get("capacity"),
            "userQueued": admission.get("userQueued"),
            "backgroundQueued": admission.get("backgroundQueued"),
            "deferred": admission.get("deferred"),
            "refused": admission.get("refused"),
            "busyAfterAdmission": admission.get("busyAfterAdmission"),
            "recoveryHold": admission.get("recoveryHold"),
            "recoveryPause": admission.get("recoveryPause"),
        },
        "plans": {
            "measured": plans.get("measured"),
            "overruns": plans.get("overruns"),
            "overrunsByClass": plans.get("overrunsByClass"),
            "overrunsByDimension": plans.get("overrunsByDimension"),
            "largest": plans.get("largest"),
        },
        "pressure": {
            "yields": pressure.get("yields"),
            "foregroundActive": pressure.get("foregroundActive"),
            "healthy": pressure.get("healthy"),
        },
        "health": {
            "state": health.get("state"),
            "consecutiveFailures": health.get("consecutiveFailures"),
            "lastError": health.get("lastError"),
            "recycles": health.get("handleRecycles"),
            "backgroundPaused": health.get("backgroundPaused"),
        },
        "process": {
            "goroutines": process.get("goroutines"),
            "heapAllocBytes": process.get("heapAllocBytes"),
            "nextGCBytes": process.get("nextGCBytes"),
            "gcCycles": process.get("gcCycles"),
            "containedPanics": process.get("containedPanics"),
        },
        "publications": document.get("publications"),
        "ungatedWrites": document.get("ungatedWrites"),
    }


class Sampler:
    """Samples the real owner diagnostics document; no background thread."""

    def __init__(self, fetch, clock=time.monotonic):
        self._fetch = fetch
        self._clock = clock
        self.rows: list[dict] = []

    def sample(self) -> dict:
        document = self._fetch()
        if not isinstance(document, dict):
            raise Refused("diagnostics document is not an object")
        row = compact_diagnostics(document)
        row["monotonicMs"] = int(self._clock() * 1000)
        self.rows.append(row)
        return row

    def summary(self) -> dict:
        return summarize(self.rows)


def _first(rows: list[dict]) -> dict:
    return rows[0] if rows else {}


def _last(rows: list[dict]) -> dict:
    return rows[-1] if rows else {}


def _delta(rows: list[dict], path: tuple[str, ...]) -> int | None:
    def get(row: dict):
        value = row
        for key in path:
            if not isinstance(value, dict):
                return None
            value = value.get(key)
        return value

    first, last = get(_first(rows)), get(_last(rows))
    if not isinstance(first, (int, float)) or not isinstance(last, (int, float)):
        return None
    return int(last - first)


def summarize(rows: list[dict]) -> dict:
    if not rows:
        return {"samples": 0, "unsupported": ["no diagnostics samples"]}
    wal_peak = max(
        int(row["wal"].get("walPeakFileBytes") or 0) for row in rows
    )
    wal_current_peak = max(int(row["wal"].get("walFileBytes") or 0) for row in rows)
    physical_peak = max(wal_peak, wal_current_peak)
    log_frames_peak = max(int(row["wal"].get("logFrames") or 0) for row in rows)
    backlog_peak = max(int(row["wal"].get("backlogFrames") or 0) for row in rows)
    checkpoints = [row["checkpoints"] for row in rows if isinstance(row.get("checkpoints"), dict)]
    max_held = max(int((row.get("gate") or {}).get("maxHeldMillis") or 0) for row in rows)
    held_delta = _delta(rows, ("gate", "heldMillis"))
    by_class_max: dict[str, int] = {}
    for row in rows:
        for name, value in (row.get("gate") or {}).get("byClass", {}).items():
            if not isinstance(value, dict):
                continue
            by_class_max[name] = max(by_class_max.get(name, 0), int(value.get("maxHeldMillis") or 0))
    # Occurrence approximation: a class crossing 250 ms between two samples counts once.
    occurrences = 0
    previous: dict[str, int] = {}
    for row in rows:
        for name, value in (row.get("gate") or {}).get("byClass", {}).items():
            if not isinstance(value, dict):
                continue
            current = int(value.get("currentHeldMillis") or 0)
            if current >= HOLD_FINDING_MILLIS and previous.get(name, 0) < HOLD_FINDING_MILLIS:
                occurrences += 1
            previous[name] = current
    plan_overruns = _delta(rows, ("plans", "overruns"))
    admission_refused = _delta(rows, ("admission", "refused"))
    busy_after = _delta(rows, ("admission", "busyAfterAdmission"))
    checkpoint_errors = _delta(rows, ("checkpoints", "errors"))
    checkpoint_truncates = _delta(rows, ("checkpoints", "truncates"))
    checkpoint_busy = _delta(rows, ("checkpoints", "busy"))
    recovery_hold = any(bool((row.get("admission") or {}).get("recoveryHold")) for row in rows)
    recovery_pause = any(bool((row.get("admission") or {}).get("recoveryPause")) for row in rows)
    health_states = sorted({str((row.get("health") or {}).get("state")) for row in rows if (row.get("health") or {}).get("state")})
    return {
        "samples": len(rows),
        "walPhysicalPeakBytes": physical_peak,
        "walPeakBasis": "max(process-lifetime walPeakFileBytes, sampled walFileBytes); process peak is conservative for this window",
        "walFilePeakBytes": wal_current_peak,
        "walProcessPeakBytes": wal_peak,
        "walLogFramesPeak": log_frames_peak,
        "walBacklogFramesPeak": backlog_peak,
        "walBusySamples": sum(1 for row in rows if (row["wal"].get("busy") or 0) != 0),
        "walLastErrors": sorted({str(row["wal"].get("lastError")) for row in rows if row["wal"].get("lastError")}),
        "checkpointAttemptsDelta": _delta(rows, ("checkpoints", "attempts")),
        "checkpointTruncatesDelta": checkpoint_truncates,
        "checkpointBusyDelta": checkpoint_busy,
        "checkpointErrorsDelta": checkpoint_errors,
        "checkpointPressureTimeoutsDelta": _delta(rows, ("checkpoints", "pressureTimeouts")),
        "checkpointMaxDurationMs": max(int(value.get("maxDurationMs") or 0) for value in checkpoints) if checkpoints else 0,
        "gateMaxHeldMillis": max_held,
        "gateHeldMillisDelta": held_delta,
        "byClassMaxHeldMillis": by_class_max,
        "sampledHoldsAtOrAbove250ms": occurrences,
        "planOverrunsDelta": plan_overruns,
        "planOverrunsByClass": _last(rows).get("plans", {}).get("overrunsByClass"),
        "planLargest": _last(rows).get("plans", {}).get("largest"),
        "admissionRefusedDelta": admission_refused,
        "admissionBusyAfterAdmissionDelta": busy_after,
        "admissionQueuedMax": max(
            int((row.get("admission") or {}).get("userQueued") or 0) for row in rows
        ),
        "admissionBackgroundQueuedMax": max(
            int((row.get("admission") or {}).get("backgroundQueued") or 0) for row in rows
        ),
        "recoveryHoldSeen": recovery_hold,
        "recoveryPauseSeen": recovery_pause,
        "pressureYieldsDelta": _delta(rows, ("pressure", "yields")),
        "foregroundActiveSamples": sum(1 for row in rows if (row.get("pressure") or {}).get("foregroundActive")),
        "healthStates": health_states,
        "containedPanicsMax": max(int((row.get("process") or {}).get("containedPanics") or 0) for row in rows),
        "publicationsDelta": _delta(rows, ("publications",)),
        "ungatedWritesDelta": _delta(rows, ("ungatedWrites", "writer")),
        "readersAdmissionRefusedDelta": _delta(rows, ("readers", "admissionRefused")),
    }


def evaluate_thresholds(summary: dict, *, context: str) -> list[dict]:
    checks: list[dict] = []

    def add(check_id: str, status: str, detail: str, value=None) -> None:
        checks.append({"id": check_id, "status": status, "detail": detail, "value": value})

    peak = int(summary.get("walPhysicalPeakBytes") or 0)
    add(
        "wal.physicalPeakWithin192MiB",
        "pass" if peak <= WAL_PHYSICAL_LIMIT_BYTES else "fail",
        f"physical WAL peak {peak} bytes against the published 192 MiB bound",
        peak,
    )
    busy_after = summary.get("admissionBusyAfterAdmissionDelta")
    if busy_after is None:
        add("writer.busyAfterAdmission", "unavailable", "admission counters were not sampled")
    else:
        add(
            "writer.busyAfterAdmission",
            "pass" if busy_after == 0 else "fail",
            "BUSY/LOCKED after admission is an outside opener or a discipline defect, never ordinary contention",
            busy_after,
        )
    refused = summary.get("admissionRefusedDelta")
    if refused is None:
        add("writer.admissionRefused", "unavailable", "admission refusal counter was not sampled")
    else:
        add(
            "writer.admissionRefused",
            "pass" if refused == 0 else "fail",
            "a named user write ticket was refused during this window",
            refused,
        )
    errors = summary.get("checkpointErrorsDelta")
    if errors is None:
        add("wal.checkpointErrors", "unavailable", "checkpoint counters were not sampled")
    else:
        add(
            "wal.checkpointErrors",
            "pass" if errors == 0 else "fail",
            "a real checkpoint error was counted in this window",
            errors,
        )
    holds = int(summary.get("sampledHoldsAtOrAbove250ms") or 0)
    max_held = int(summary.get("gateMaxHeldMillis") or 0)
    if holds:
        hold_status = "finding"
        hold_detail = (
            "sampled hold crossings at or above 250 ms; an unhappy path, not a product refusal. "
            "The per-occurrence log line is a SRV023/P2-52b path, not a general writer diagnostic."
        )
    elif max_held >= HOLD_FINDING_MILLIS:
        hold_status = "finding"
        hold_detail = (
            "the process-lifetime max hold is at or above 250 ms, but this window observed no crossing. "
            "Attribution to this window is unproven; the peak is reported rather than passed."
        )
    else:
        hold_status = "pass"
        hold_detail = "no sampled or process-lifetime hold reached 250 ms"
    add(
        "writer.holdAtOrAbove250ms",
        hold_status,
        hold_detail,
        {"sampledOccurrences": holds, "processMaxHeldMillis": max_held},
    )
    overruns = summary.get("planOverrunsDelta")
    if overruns is None:
        add("writer.planOverruns", "unavailable", "write-plan counters were not sampled")
    else:
        add(
            "writer.planOverruns",
            "pass" if overruns == 0 else "finding",
            "a write exceeded its telemetry plan; a structural overrun is a redesign finding",
            overruns,
        )
    if summary.get("recoveryHoldSeen") or summary.get("recoveryPauseSeen"):
        add(
            "wal.recoveryHoldOrPause",
            "finding",
            "a hard-pressure recovery hold/pause was observed; a bounded reset is allowed, "
            "but user progress during it needs the load-driver window",
            {"hold": bool(summary.get("recoveryHoldSeen")), "pause": bool(summary.get("recoveryPauseSeen"))},
        )
    else:
        add("wal.recoveryHoldOrPause", "pass", "no hard-pressure recovery hold/pause was sampled")
    add(
        "wal.reservationLedger",
        "unavailable",
        "before-BEGIN debits, reservation overruns and unknown VFS I/O are internal "
        "(dbwork.WALReservations) and are not on the owner diagnostics wire in this pin",
    )
    add(
        "diagnostics.collectorQueue",
        "unavailable",
        "there is no per-collector queue diagnostic; GC cycles/heap and storage-helper stats are "
        "sampled instead, and are not a collector-work proof",
    )
    add(
        "diagnostics.userWriteLatency",
        "unavailable",
        f"this {context} window generates no user writes; scheduled/manual write latency needs the "
        "real mixed-client load driver with a matching operator-window receipt",
    )
    return checks


def check_summary(checks: list[dict]) -> dict:
    out = {"pass": 0, "finding": 0, "fail": 0, "unavailable": 0}
    for check in checks:
        status = check.get("status")
        if status in out:
            out[status] += 1
    return out


def clean_pass(checks: list[dict]) -> bool:
    summary = check_summary(checks)
    return summary["fail"] == 0 and summary["finding"] == 0 and summary["unavailable"] == 0


def overall(checks: list[dict]) -> str:
    if any(check["status"] == "fail" for check in checks):
        return "fail"
    return "pass"


def now_iso() -> str:
    import datetime

    return datetime.datetime.now(datetime.timezone.utc).isoformat().replace("+00:00", "Z")
