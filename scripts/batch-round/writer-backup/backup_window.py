"""Real scheduled/manual backup orchestration for the P2-65 batch round.

The orchestrator changes one real maintenance window so the shipped scheduler
creates a scheduled backup now, waits for scheduled-kind evidence, starts one
manual backup through the same owner API the console uses, samples the real
owner diagnostics for holds/collector/WAL/writer observations, and always
restores the original maintenance settings in a finally block.
"""

from __future__ import annotations

import datetime
import time
from dataclasses import dataclass

from common import (
    BACKUPS_PATH,
    DIAGNOSTICS_PATH,
    MAINTENANCE_PATH,
    SCHEDULED_NO_PROGRESS_BUDGET_MILLIS,
    ApiError,
    Refused,
    Sampler,
    check_summary,
    clean_pass,
    evaluate_thresholds,
    now_iso,
    overall,
    read_private_json,
    redact_restore,
    redact_settings,
    remove_quietly,
    scheduled_no_progress_tolerance_ms,
    write_private_json,
)

DEFAULT_POLL_SECONDS = 1.0
# The window is opened a couple of minutes in the past so ordinary clock skew
# between the operator host and the server cannot make the occurrence closed.
# This changes only the scheduling hint, never a product deadline.
WINDOW_START_BACKDATE_MINUTES = 2


@dataclass(frozen=True)
class BackupWindowOptions:
    window_id: str
    out: str
    window_minutes: int = 45
    scheduled_timeout: float = 360.0
    manual_timeout: float = 600.0
    poll_seconds: float = DEFAULT_POLL_SECONDS
    keep_count: int | None = None
    load_receipt: str | None = None
    restore_plan: str | None = None
    manual_only: bool = False


def _utc_now() -> datetime.datetime:
    return datetime.datetime.now(datetime.timezone.utc)


def _window_key(now: datetime.datetime) -> tuple[str, int]:
    start = now - datetime.timedelta(minutes=WINDOW_START_BACKDATE_MINUTES)
    return start.strftime("%A").lower(), start.hour * 60 + start.minute


def build_schedule(settings: dict, now: datetime.datetime, minutes: int) -> tuple[dict, dict]:
    windows = settings.get("windows")
    if not isinstance(windows, list) or not windows:
        raise Refused("maintenance settings have no windows")
    by_id = {window.get("id"): window for window in windows if isinstance(window, dict)}
    if "nightly" not in by_id or "heavy" not in by_id:
        raise Refused("maintenance settings must keep the nightly and heavy windows")
    if len(windows) != 2:
        raise Refused("maintenance settings must contain exactly two windows")
    day, start_minute = _window_key(now)
    nightly = dict(by_id["nightly"])
    nightly.update(
        {
            "id": "nightly",
            "name": nightly.get("name") or "Backups",
            "enabled": True,
            "cadence": "custom",
            "days": [day],
            "startMinute": start_minute,
            "durationMinutes": max(15, min(1440, int(minutes))),
            "timezone": "UTC",
            "tasks": ["backup"],
        }
    )
    changed = dict(settings)
    changed["windows"] = [nightly if window.get("id") == "nightly" else window for window in windows]
    return changed, nightly


def _settings_revision(document: dict) -> int:
    revision = document.get("revision")
    if not isinstance(revision, int) or revision < 0:
        raise Refused("maintenance document has no integer revision")
    return revision


def _window_start(now: datetime.datetime) -> str:
    start = (now - datetime.timedelta(minutes=WINDOW_START_BACKDATE_MINUTES)).replace(second=0, microsecond=0)
    return start.isoformat().replace("+00:00", "Z")


def _time_at_or_after(value: str, floor: str) -> bool:
    try:
        return datetime.datetime.fromisoformat(value.replace("Z", "+00:00")) >= datetime.datetime.fromisoformat(
            floor.replace("Z", "+00:00")
        )
    except ValueError:
        # Same-format RFC3339 UTC strings order correctly; never treat an
        # unparseable value as covering.
        return value >= floor


def _backup_ids(document: dict) -> set[str]:
    return {entry.get("id") for entry in document.get("backups") or [] if isinstance(entry, dict)}


def _job_id(document: dict) -> str | None:
    last = document.get("lastBackup")
    if isinstance(last, dict) and isinstance(last.get("jobId"), str):
        return last["jobId"]
    return None


def _restore_key(result: dict | None) -> tuple:
    if not isinstance(result, dict):
        return ()
    return tuple(result.get(key) for key in ("operationId", "jobId", "outcome", "at"))


class BackupWindowRun:
    def __init__(
        self,
        api,
        options: BackupWindowOptions,
        *,
        clock=time.monotonic,
        sleep=time.sleep,
        now=_utc_now,
    ):
        self.api = api
        self.options = options
        self.clock = clock
        self.sleep = sleep
        self.now = now
        self.sampler = Sampler(lambda: self.api.request("GET", DIAGNOSTICS_PATH), clock=clock)
        self.window_now = None
        self.original_settings: dict | None = None
        self.original_revision: int | None = None
        self.settings_changed = False
        self.restore_plan_path: str | None = options.restore_plan or (options.out + ".restore-plan.json")
        self.receipt: dict = {}

    # ---------------------------------------------------------------- prepare
    def _read_baseline(self) -> None:
        self.window_now = self.now()
        maintenance = self.api.request("GET", MAINTENANCE_PATH)
        if not isinstance(maintenance, dict):
            raise Refused("maintenance document is not an object")
        self.original_revision = _settings_revision(maintenance)
        self.original_settings = redact_settings(maintenance)
        backups = self.api.request("GET", BACKUPS_PATH)
        if not isinstance(backups, dict):
            raise Refused("backups document is not an object")
        if backups.get("running"):
            raise Refused("a backup is already running; this window refuses to join or disturb it")
        if backups.get("restore"):
            raise Refused("a restore is staged or running; finish it before this window")
        self.baseline_ids = _backup_ids(backups)
        self.baseline_last_job = _job_id(backups)
        self.previous_restore = redact_restore(backups.get("lastRestore"))
        window_start = _window_start(self.window_now)
        covering = [
            entry
            for entry in backups.get("backups") or []
            if isinstance(entry, dict)
            and entry.get("kind") == "scheduled"
            and isinstance(entry.get("createdAt"), str)
            and _time_at_or_after(entry["createdAt"], window_start)
        ]
        if covering and not self.options.manual_only:
            raise Refused(
                "a scheduled backup already covers the proposed occurrence; the scheduler will not "
                "start another one. Run later or use the existing occurrence."
            )
        self.receipt.update(
            {
                "kind": "p2-65-backup-window",
                "version": 1,
                "windowId": self.options.window_id,
                "manualOnly": self.options.manual_only,
                "startedAt": now_iso(),
                "maintenance": {
                    "originalRevision": self.original_revision,
                    "originalSettings": self.original_settings,
                    "windowStart": _window_start(self.window_now),
                },
                "backups": {
                    "baselineIds": sorted(item for item in self.baseline_ids if item),
                    "baselineLastJobId": self.baseline_last_job,
                    "previousRestore": self.previous_restore,
                },
            }
        )

    def _apply_schedule(self) -> dict:
        changed, window = build_schedule(self.original_settings, self.window_now, self.options.window_minutes)
        if self.options.keep_count is not None:
            changed["backupKeepCount"] = max(1, min(365, int(self.options.keep_count)))
        # The inverse is durable before the change. A killed process leaves this
        # file for the operator's --restore-only pass.
        try:
            write_private_json(
                self.restore_plan_path,
                {
                    "kind": "p2-65-maintenance-restore-plan",
                    "version": 1,
                    "windowId": self.options.window_id,
                    "createdAt": now_iso(),
                    "originalRevision": self.original_revision,
                    "originalSettings": self.original_settings,
                },
            )
        except OSError as error:
            raise Refused(
                f"a restore plan already exists at {self.restore_plan_path}; "
                "run --restore-only against it before starting another window"
            ) from error
        # From here the settings may already have changed, even if the response
        # is lost. The finally block always reconciles back to the original.
        self.settings_changed = True
        applied = self.api.request(
            "PUT",
            MAINTENANCE_PATH,
            {
                "expectedRevision": self.original_revision,
                "operationId": f"{self.options.window_id}-set",
                "settings": changed,
            },
        )
        if not isinstance(applied, dict):
            raise Refused("maintenance update did not answer a document")
        self.receipt["maintenance"].update(
            {
                "appliedRevision": _settings_revision(applied),
                "scheduledWindow": window,
                "keepCountChanged": self.options.keep_count is not None,
            }
        )
        return applied

    # ---------------------------------------------------------------- observe
    def _observe_scheduled(self) -> dict:
        deadline = self.clock() + self.options.scheduled_timeout
        observed_job: str | None = None
        started: float | None = None
        last_progress: float | None = None
        progress_key = None
        phases: list[str] = []
        while self.clock() < deadline:
            document = self.api.request("GET", BACKUPS_PATH)
            self.sampler.sample()
            running = document.get("running")
            if isinstance(running, dict) and running.get("kind") == "scheduled":
                if observed_job is None:
                    observed_job = running.get("jobId")
                    started = self.clock()
                    last_progress = started
                phase = running.get("phase")
                if isinstance(phase, str) and (not phases or phases[-1] != phase):
                    phases.append(phase)
                key = (running.get("phase"), running.get("bytesDone"))
                if key != progress_key:
                    progress_key = key
                    last_progress = self.clock()
            last = document.get("lastBackup")
            if observed_job and isinstance(last, dict) and last.get("jobId") == observed_job:
                return self._scheduled_result("running", observed_job, started, last_progress, phases, last)
            for entry in document.get("backups") or []:
                if not isinstance(entry, dict) or entry.get("kind") != "scheduled":
                    continue
                if entry.get("id") in self.baseline_ids:
                    continue
                # A completed scheduled copy is its own kind evidence.
                return self._scheduled_result(
                    "listed",
                    observed_job,
                    None,
                    None,
                    phases,
                    {
                        "jobId": None,
                        "outcome": "completed",
                        "backupId": entry.get("id"),
                        "createdAt": entry.get("createdAt"),
                    },
                )
            if observed_job is None:
                last_job = _job_id(document)
                if last_job and last_job != self.baseline_last_job and not running:
                    # A job finished before any running sample. Its kind was never
                    # observed, so it is not scheduled-kind evidence.
                    return {
                        "kindEvidence": "missing",
                        "jobId": last_job,
                        "detail": "a backup finished before scheduled kind was observed",
                    }
            self.sleep(self.options.poll_seconds)
        return {"kindEvidence": "timeout", "detail": "no scheduled backup was observed in the window"}

    def _scheduled_result(
        self,
        evidence: str,
        job: str | None,
        started: float | None,
        last_progress: float | None,
        phases: list[str],
        last: dict,
    ) -> dict:
        total = None if started is None else int((self.clock() - started) * 1000)
        no_progress = None if last_progress is None else int((self.clock() - last_progress) * 1000)
        return {
            "kindEvidence": evidence,
            "jobId": job or last.get("jobId"),
            "backupId": last.get("backupId"),
            "createdAt": last.get("createdAt"),
            "phases": phases,
            "totalObservedMs": total,
            "noProgressObservedMs": no_progress,
            "durationBasis": "listed" if evidence == "listed" else ("last-progress" if last_progress is not None else "first-observation"),
            "outcome": last.get("outcome"),
            "errorCode": last.get("errorCode"),
        }

    def _wait_idle(self, timeout: float = 30.0) -> None:
        deadline = self.clock() + timeout
        while self.clock() < deadline:
            document = self.api.request("GET", BACKUPS_PATH)
            if not document.get("running"):
                return
            self.sampler.sample()
            self.sleep(self.options.poll_seconds)
        raise Refused("a backup is still running; the manual step refuses to collide with it")

    def _run_manual(self) -> dict:
        accepted = self.api.request(
            "POST",
            BACKUPS_PATH,
            {"operationId": f"{self.options.window_id}-manual"},
        )
        accepted_status = getattr(self.api, "last_status", 202)
        if not isinstance(accepted, dict) or not isinstance(accepted.get("jobId"), str):
            raise Refused("manual backup start did not answer a job id")
        job = accepted["jobId"]
        started = self.clock()
        started_wall = now_iso()
        deadline = self.clock() + self.options.manual_timeout
        while self.clock() < deadline:
            document = self.api.request("GET", BACKUPS_PATH)
            self.sampler.sample()
            last = document.get("lastBackup")
            if isinstance(last, dict) and last.get("jobId") == job:
                return {
                    "jobId": job,
                    "acceptedStatus": accepted_status,
                    "outcome": last.get("outcome"),
                    "errorCode": last.get("errorCode"),
                    "backupId": last.get("backupId"),
                    "startedAt": started_wall,
                    "finishedAt": now_iso(),
                    "observedElapsedMs": int((self.clock() - started) * 1000),
                }
            self.sleep(self.options.poll_seconds)
        return {
            "jobId": job,
            "acceptedStatus": accepted_status,
            "outcome": "timeout",
            "errorCode": "manual_timeout",
            "startedAt": started_wall,
            "finishedAt": now_iso(),
            "observedElapsedMs": int((self.clock() - started) * 1000),
        }

    # ----------------------------------------------------------------- restore
    def _restore_original(self) -> dict:
        attempts = 0
        last_error: str | None = None
        for attempt in range(3):
            attempts += 1
            try:
                current = self.api.request("GET", MAINTENANCE_PATH)
                if redact_settings(current) == self.original_settings:
                    remove_quietly(_path(self.restore_plan_path))
                    return {"restored": True, "attempts": attempts, "revision": _settings_revision(current)}
                result = self.api.request(
                    "PUT",
                    MAINTENANCE_PATH,
                    {
                        "expectedRevision": _settings_revision(current),
                        "operationId": f"{self.options.window_id}-restore-{attempt}",
                        "settings": self.original_settings,
                    },
                )
                if isinstance(result, dict):
                    remove_quietly(_path(self.restore_plan_path))
                    return {"restored": True, "attempts": attempts, "revision": _settings_revision(result)}
            except ApiError as error:
                last_error = error.code
            except Refused as error:
                last_error = str(error)
            self.sleep(1.0 + attempt)
        return {
            "restored": False,
            "attempts": attempts,
            "errorCode": last_error or "restore_not_confirmed",
            "plan": self.restore_plan_path,
        }

    # -------------------------------------------------------------------- run
    def execute(self) -> dict:
        self._read_baseline()
        checks: list[dict] = []
        failure = None
        try:
            if not self.options.manual_only:
                self._apply_schedule()
                self.sampler.sample()
                scheduled = self._observe_scheduled()
                self.receipt["backups"]["scheduled"] = scheduled
                checks.append(_scheduled_check(scheduled, self.options.poll_seconds))
                if checks[-1]["status"] == "fail":
                    failure = "scheduled"
            else:
                self.receipt["backups"]["scheduled"] = {"kindEvidence": "skipped", "detail": "manual-only window"}
                self.sampler.sample()
            if failure is None:
                self._wait_idle()
                try:
                    manual = self._run_manual()
                except ApiError as error:
                    manual = {
                        "acceptedStatus": error.status,
                        "outcome": "refused",
                        "errorCode": error.code,
                        "detail": "manual start was refused; a capacity refusal is a failure",
                    }
                self.receipt["backups"]["manual"] = manual
                checks.append(_manual_check(manual))
                if checks[-1]["status"] == "fail":
                    failure = "manual"
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
            if self.settings_changed:
                restored = self._restore_original()
                self.receipt["maintenance"]["restore"] = restored
                if not restored.get("restored"):
                    failure = "restore_failed"
                    checks.append(
                        {
                            "id": "maintenance.restore",
                            "status": "fail",
                            "detail": "the original maintenance settings were not confirmed restored",
                            "value": restored,
                        }
                    )
                else:
                    checks.append(
                        {
                            "id": "maintenance.restore",
                            "status": "pass",
                            "detail": "the original maintenance settings were restored",
                            "value": restored.get("attempts"),
                        }
                    )
            after = None
            try:
                after = self.api.request("GET", BACKUPS_PATH)
            except ApiError:
                after = None
            current_restore = redact_restore(after.get("lastRestore")) if isinstance(after, dict) else None
            reread = isinstance(after, dict)
            preserved = reread and _restore_key(current_restore) == _restore_key(self.previous_restore)
            self.receipt["backups"]["afterRestore"] = current_restore
            self.receipt["backups"]["previousRestorePreserved"] = preserved
            checks.append(
                {
                    "id": "restore.previousPreserved",
                    "status": "pass" if preserved else "fail",
                    "detail": "the last-restore result is independent of a backup window and must be re-read after it",
                }
            )
            if not preserved:
                failure = failure or "previous_restore_changed"
            summary = self.sampler.summary()
            self.receipt["diagnostics"] = summary
            checks.extend(evaluate_thresholds(summary, context="backup"))
            self.receipt["thresholds"] = checks
            try:
                self.receipt["loadCorrelation"] = _load_correlation(self.options.load_receipt, self.receipt)
            except (Refused, OSError, ValueError) as error:
                self.receipt["loadCorrelation"] = {"provided": True, "error": str(error)}
            correlation = self.receipt["loadCorrelation"]
            if not correlation.get("provided"):
                checks.append(
                    {
                        "id": "load.windowOverlap",
                        "status": "unavailable",
                        "detail": "no load receipt was supplied; run load.ts with the matching --operator-window",
                    }
                )
            elif correlation.get("error"):
                checks.append(
                    {
                        "id": "load.windowOverlap",
                        "status": "unavailable",
                        "detail": f"load receipt could not be read: {correlation['error']}",
                    }
                )
            else:
                overlap = correlation.get("backupOverlapMs")
                checks.append(
                    {
                        "id": "load.windowOverlap",
                        "status": "pass" if isinstance(overlap, int) and overlap > 0 else "fail",
                        "detail": "the load receipt and the scheduled/manual backup must share at least one millisecond",
                        "value": {"basis": correlation.get("overlapBasis"), "overlapMs": overlap},
                    }
                )
            self.receipt["unsupported"] = [
                "wal-reservation-ledger: internal; not on the owner diagnostics wire",
                "per-occurrence-hold-log: SRV023/P2-52b path, not a general writer diagnostic",
                "collector-queue: no per-collector queue diagnostic; GC/heap sampled",
                "user-write-latency: needs the mixed-client load driver, not this orchestrator",
                "retention-prune: the scheduler prunes after success; this window observes it, not a separate deletion proof",
            ]
            self.receipt["finishedAt"] = now_iso()
            if failure == "restore_failed":
                self.receipt["result"] = "restore_failed"
            elif failure:
                self.receipt["result"] = "fail"
            else:
                self.receipt["result"] = overall(checks)
            self.receipt["checkSummary"] = check_summary(checks)
            self.receipt["cleanPass"] = clean_pass(checks)
            self.receipt["claim"] = (
                "clean-pass"
                if self.receipt["cleanPass"]
                else "gate-pass-with-findings-or-unavailable"
            )
        return self.receipt


def _path(value: str):
    from pathlib import Path

    return Path(value)


def _scheduled_check(scheduled: dict, poll_seconds: float) -> dict:
    evidence = scheduled.get("kindEvidence")
    if evidence not in ("running", "listed"):
        return {
            "id": "backup.scheduledKindEvidence",
            "status": "fail",
            "detail": f"scheduled-kind evidence was {evidence}",
        }
    outcome = scheduled.get("outcome")
    if outcome == "completed":
        return {
            "id": "backup.scheduledKindEvidence",
            "status": "pass",
            "detail": "a scheduled-kind backup completed",
            "value": {"totalObservedMs": scheduled.get("totalObservedMs")},
        }
    if outcome == "failed" and scheduled.get("errorCode") == "backup_busy":
        measured = scheduled.get("noProgressObservedMs")
        basis = scheduled.get("durationBasis")
        if measured is None:
            measured = scheduled.get("totalObservedMs")
            basis = "first-observation"
        if measured is None:
            return {
                "id": "backup.scheduledKindEvidence",
                "status": "fail",
                "detail": "backup_busy is the documented outcome, but neither a progress nor a total window was observed; the 60 s gate is unproven",
            }
        tolerance = scheduled_no_progress_tolerance_ms(poll_seconds)
        if SCHEDULED_NO_PROGRESS_BUDGET_MILLIS <= measured <= SCHEDULED_NO_PROGRESS_BUDGET_MILLIS + tolerance:
            return {
                "id": "backup.scheduledKindEvidence",
                "status": "pass",
                "detail": f"backup_busy after {measured} ms by {basis} matches the approved 60 s no-progress budget "
                f"(sampler tolerance {tolerance} ms)",
                "value": {"measuredMs": measured, "basis": basis, "toleranceMs": tolerance},
            }
        return {
            "id": "backup.scheduledKindEvidence",
            "status": "fail",
            "detail": f"backup_busy after {measured} ms by {basis} is outside the approved 60 s no-progress budget "
            f"(accepted up to {SCHEDULED_NO_PROGRESS_BUDGET_MILLIS + tolerance} ms with sampler tolerance)",
            "value": {"measuredMs": measured, "basis": basis, "toleranceMs": tolerance},
        }
    return {
        "id": "backup.scheduledKindEvidence",
        "status": "fail",
        "detail": f"scheduled backup ended {outcome} {scheduled.get('errorCode') or ''}".strip(),
    }


def _manual_check(manual: dict) -> dict:
    if manual.get("outcome") == "completed" and manual.get("acceptedStatus") == 202:
        return {
            "id": "backup.manualNoCapacityRefusal",
            "status": "pass",
            "detail": f"manual backup accepted ({manual.get('acceptedStatus')}) and completed in {manual.get('observedElapsedMs')} ms",
        }
    return {
        "id": "backup.manualNoCapacityRefusal",
        "status": "fail",
        "detail": f"manual backup {manual.get('outcome')} {manual.get('errorCode') or ''}".strip(),
    }


def _load_correlation(path: str | None, receipt: dict) -> dict:
    if not path:
        return {
            "provided": False,
            "detail": "run load.ts with --label scheduled-backup/manual-backup and "
            f"--operator-window {receipt.get('windowId')}",
        }
    from common import sha256_file

    document = read_private_json(path)
    checksum = sha256_file(_path(path))
    started = document.get("startedAt")
    elapsed = document.get("elapsedSeconds")
    overlap = None
    basis = None
    if isinstance(started, str) and isinstance(elapsed, (int, float)):
        try:
            load_start = datetime.datetime.fromisoformat(started.replace("Z", "+00:00"))
            load_end = load_start + datetime.timedelta(seconds=float(elapsed))
            scheduled = receipt.get("backups", {}).get("scheduled") or {}
            manual = receipt.get("backups", {}).get("manual") or {}
            backup_start = backup_end = None
            if scheduled.get("kindEvidence") != "skipped" and isinstance(scheduled.get("createdAt"), str):
                backup_end = datetime.datetime.fromisoformat(scheduled["createdAt"].replace("Z", "+00:00"))
                total = scheduled.get("totalObservedMs")
                backup_start = (
                    backup_end - datetime.timedelta(milliseconds=total)
                    if isinstance(total, int) and total >= 0
                    else load_start
                )
                basis = "scheduled"
            elif isinstance(manual.get("startedAt"), str) and isinstance(manual.get("finishedAt"), str):
                backup_start = datetime.datetime.fromisoformat(manual["startedAt"].replace("Z", "+00:00"))
                backup_end = datetime.datetime.fromisoformat(manual["finishedAt"].replace("Z", "+00:00"))
                basis = "manual"
            if backup_start is not None and backup_end is not None:
                overlap = max(0, int((min(load_end, backup_end) - max(load_start, backup_start)).total_seconds() * 1000))
        except ValueError:
            overlap = None
    return {
        "provided": True,
        "sha256": checksum,
        "label": document.get("label"),
        "operatorWindow": document.get("operatorWindow"),
        "passed": document.get("passed"),
        "startedAt": started,
        "elapsedSeconds": elapsed,
        "overlapBasis": basis,
        "backupOverlapMs": overlap,
        "note": "load thresholds are the driver's own gates; this file only correlates the windows",
    }


def restore_only(api, plan_path: str, *, sleep=time.sleep) -> dict:
    plan = read_private_json(plan_path)
    if plan.get("kind") != "p2-65-maintenance-restore-plan":
        raise Refused("restore plan has the wrong kind")
    original = plan.get("originalSettings")
    if not isinstance(original, dict):
        raise Refused("restore plan has no original settings")
    last: str | None = None
    for attempt in range(5):
        try:
            current = api.request("GET", MAINTENANCE_PATH)
            if redact_settings(current) == original:
                remove_quietly(_path(plan_path))
                return {"restored": True, "attempts": attempt + 1, "revision": _settings_revision(current)}
            result = api.request(
                "PUT",
                MAINTENANCE_PATH,
                {
                    "expectedRevision": _settings_revision(current),
                    "operationId": f"{plan.get('windowId')}-restore-only-{attempt}",
                    "settings": original,
                },
            )
            remove_quietly(_path(plan_path))
            return {"restored": True, "attempts": attempt + 1, "revision": _settings_revision(result)}
        except ApiError as error:
            last = error.code
        sleep(1.0 + attempt)
    return {"restored": False, "errorCode": last or "restore_not_confirmed", "plan": plan_path}


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
        self.calls: list[tuple[str, str, dict | None]] = []

    def request(self, method: str, path: str, body: dict | None = None):
        self.calls.append((method, path, body))
        return self.handler(method, path, body)


def _maintenance_document(revision: int, settings: dict) -> dict:
    return {"revision": revision, "settings": settings}


def _base_settings() -> dict:
    return {
        "windows": [
            {
                "id": "nightly",
                "name": "Backups",
                "enabled": True,
                "cadence": "nightly",
                "days": ["monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"],
                "startMinute": 180,
                "durationMinutes": 240,
                "timezone": "",
                "tasks": ["backup"],
            },
            {
                "id": "heavy",
                "name": "Heavy background work",
                "enabled": False,
                "cadence": "nightly",
                "days": ["monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"],
                "startMinute": 60,
                "durationMinutes": 300,
                "timezone": "",
                "tasks": ["analysis", "trickplay"],
            },
        ],
        "retention": {"logs": 30},
        "backupKeepCount": 7,
        "backgroundSpeed": "normal",
    }


def _diagnostics(peak: int = 1024) -> dict:
    return {
        "wal": {
            "walFileBytes": peak,
            "walPeakFileBytes": peak,
            "logFrames": 10,
            "checkpointedFrames": 10,
            "backlogFrames": 0,
            "busy": 0,
            "readers": {"snapshots": 1, "admissionRefused": 0},
            "checkpoints": {"attempts": 2, "truncates": 0, "busy": 0, "errors": 0, "pressureTimeouts": 0, "maxDurationMs": 5},
        },
        "writeGate": {
            "active": False,
            "heldMillis": 100,
            "maxHeldMillis": 40,
            "queueWaitMillis": 10,
            "byClass": {"interactive": {"currentHeldMillis": 0, "maxHeldMillis": 40, "queued": 0}},
        },
        "writerAdmission": {
            "capacity": 8,
            "userQueued": 0,
            "backgroundQueued": 0,
            "deferred": 0,
            "refused": 0,
            "busyAfterAdmission": 0,
            "recoveryHold": False,
            "recoveryPause": False,
        },
        "writePlans": {"measured": 10, "overruns": 0, "overrunsByClass": {}, "overrunsByDimension": {}, "largest": {}},
        "pressure": {"yields": 1, "foregroundActive": False, "healthy": True},
        "health": {"state": "healthy", "consecutiveFailures": 0, "lastError": "", "handleRecycles": 0, "backgroundPaused": False},
        "process": {"goroutines": 20, "heapAllocBytes": 1024, "nextGCBytes": 2048, "gcCycles": 3, "containedPanics": 0},
        "publications": 5,
        "ungatedWrites": {"writer": 0, "reader": 0},
    }


def self_check() -> dict:
    import shutil
    import tempfile

    results = {}
    scratch = tempfile.mkdtemp(prefix="p2-65-backup-selfcheck-")
    try:
        return _self_check_scenarios(scratch, results)
    finally:
        shutil.rmtree(scratch, ignore_errors=True)


def _self_check_scenarios(scratch: str, results: dict) -> dict:

    def scenario_busy():
        clock = _FakeClock()
        state = {"revision": 1, "settings": _base_settings(), "phase": 0, "started": None, "applied": False}

        def handler(method, path, body):
            if method == "GET" and path == MAINTENANCE_PATH:
                return _maintenance_document(state["revision"], state["settings"])
            if method == "PUT" and path == MAINTENANCE_PATH:
                assert body["expectedRevision"] == state["revision"]
                state["revision"] += 1
                state["settings"] = body["settings"]
                state["applied"] = True
                return _maintenance_document(state["revision"], state["settings"])
            if method == "GET" and path == BACKUPS_PATH:
                if not state["applied"]:
                    return {"backups": [], "lastRestore": {"jobId": "r1", "outcome": "restored"}}
                if state["phase"] == 0:
                    state["phase"] = 1
                if state["phase"] == 1:
                    state["phase"] = 2
                    state["started"] = clock()
                    return {"backups": [], "running": {"jobId": "s1", "kind": "scheduled", "phase": "copying"}}
                if clock() - state["started"] < 61:
                    return {"backups": [], "running": {"jobId": "s1", "kind": "scheduled", "phase": "copying"}}
                if not state.get("manual"):
                    return {
                        "backups": [],
                        "lastBackup": {"jobId": "s1", "outcome": "failed", "errorCode": "backup_busy"},
                    }
                return {
                    "backups": [],
                    "lastBackup": {"jobId": "m1", "outcome": "completed"},
                    "lastRestore": {"jobId": "r1", "outcome": "restored"},
                }
            if method == "GET" and path == DIAGNOSTICS_PATH:
                return _diagnostics()
            if method == "POST" and path == BACKUPS_PATH:
                state["manual"] = True
                return {"jobId": "m1"}
            raise AssertionError((method, path))

        # The scheduled result must be produced by the running observation.
        api = _FakeApi(handler)
        options = BackupWindowOptions("self-busy", scratch + "/busy.json", scheduled_timeout=120, manual_timeout=30)
        run = BackupWindowRun(api, options, clock=clock, sleep=clock.sleep, now=lambda: datetime.datetime(2026, 10, 28, 12, 0, tzinfo=datetime.timezone.utc))
        receipt = run.execute()
        assert receipt["result"] == "pass", receipt
        assert receipt["backups"]["scheduled"]["errorCode"] == "backup_busy"
        assert 60_000 <= receipt["backups"]["scheduled"]["noProgressObservedMs"] <= 63_000
        assert receipt["backups"]["scheduled"]["durationBasis"] == "last-progress"
        assert receipt["backups"]["manual"]["outcome"] == "completed"
        assert receipt["backups"]["manual"]["acceptedStatus"] == 202
        assert receipt["backups"]["previousRestorePreserved"] is True
        assert receipt["maintenance"]["restore"]["restored"] is True
        assert any(call[0] == "PUT" and call[1] == MAINTENANCE_PATH for call in api.calls[-4:])
        return receipt["result"]

    def scenario_manual_refused():
        clock = _FakeClock()
        state = {"revision": 1, "settings": _base_settings(), "scheduled": False}

        def handler(method, path, body):
            if method == "GET" and path == MAINTENANCE_PATH:
                return _maintenance_document(state["revision"], state["settings"])
            if method == "PUT" and path == MAINTENANCE_PATH:
                state["revision"] += 1
                state["settings"] = body["settings"]
                return _maintenance_document(state["revision"], state["settings"])
            if method == "GET" and path == BACKUPS_PATH:
                if not state["scheduled"]:
                    return {"backups": [], "lastRestore": None}
                return {"backups": [{"id": "2026-10-28T120000Z", "kind": "scheduled", "createdAt": "2026-10-28T12:00:01Z"}], "lastRestore": None}
            if method == "GET" and path == DIAGNOSTICS_PATH:
                return _diagnostics()
            if method == "POST" and path == BACKUPS_PATH:
                raise ApiError(409, "insufficient_disk")
            raise AssertionError((method, path))

        api = _FakeApi(handler)
        # Mark the scheduled copy as listed only after the schedule is applied.
        original_put = handler

        def wrapped(method, path, body):
            result = original_put(method, path, body)
            if method == "PUT" and path == MAINTENANCE_PATH:
                state["scheduled"] = True
            return result

        api.handler = wrapped
        options = BackupWindowOptions("self-refused", scratch + "/refused.json", scheduled_timeout=10, manual_timeout=10)
        run = BackupWindowRun(api, options, clock=clock, sleep=clock.sleep, now=lambda: datetime.datetime(2026, 10, 28, 12, 0, tzinfo=datetime.timezone.utc))
        receipt = run.execute()
        assert receipt["result"] == "fail", receipt
        assert receipt["backups"]["manual"]["errorCode"] == "insufficient_disk"
        assert receipt["maintenance"]["restore"]["restored"] is True
        return receipt["result"]

    def scenario_scheduled_timeout():
        clock = _FakeClock()
        state = {"revision": 1, "settings": _base_settings()}

        def handler(method, path, body):
            if method == "GET" and path == MAINTENANCE_PATH:
                return _maintenance_document(state["revision"], state["settings"])
            if method == "PUT" and path == MAINTENANCE_PATH:
                state["revision"] += 1
                state["settings"] = body["settings"]
                return _maintenance_document(state["revision"], state["settings"])
            if method == "GET" and path == BACKUPS_PATH:
                return {"backups": [], "lastRestore": None}
            if method == "GET" and path == DIAGNOSTICS_PATH:
                return _diagnostics(peak=200 * 1024 * 1024)
            raise AssertionError((method, path))

        api = _FakeApi(handler)
        options = BackupWindowOptions("self-timeout", scratch + "/timeout.json", scheduled_timeout=5, manual_timeout=5)
        run = BackupWindowRun(api, options, clock=clock, sleep=clock.sleep, now=lambda: datetime.datetime(2026, 10, 28, 12, 0, tzinfo=datetime.timezone.utc))
        receipt = run.execute()
        assert receipt["result"] == "fail", receipt
        assert receipt["backups"]["scheduled"]["kindEvidence"] == "timeout"
        assert any(item["id"] == "wal.physicalPeakWithin192MiB" and item["status"] == "fail" for item in receipt["thresholds"])
        assert receipt["maintenance"]["restore"]["restored"] is True
        return receipt["result"]

    def scenario_busy_timing(progress_until: int, fail_at: int):
        clock = _FakeClock()
        state = {"revision": 1, "settings": _base_settings(), "applied": False, "started": None, "manual": False}

        def handler(method, path, body):
            if method == "GET" and path == MAINTENANCE_PATH:
                return _maintenance_document(state["revision"], state["settings"])
            if method == "PUT" and path == MAINTENANCE_PATH:
                state["revision"] += 1
                state["settings"] = body["settings"]
                state["applied"] = True
                return _maintenance_document(state["revision"], state["settings"])
            if method == "GET" and path == BACKUPS_PATH:
                if not state["applied"]:
                    return {"backups": [], "lastRestore": None}
                if state["started"] is None:
                    state["started"] = clock()
                elapsed = int(clock() - state["started"])
                if not state["manual"]:
                    if elapsed < fail_at:
                        return {
                            "backups": [],
                            "running": {
                                "jobId": "s1",
                                "kind": "scheduled",
                                "phase": "copying",
                                "bytesDone": min(elapsed, progress_until),
                            },
                        }
                    return {"backups": [], "lastBackup": {"jobId": "s1", "outcome": "failed", "errorCode": "backup_busy"}}
                return {"backups": [], "lastBackup": {"jobId": "m1", "outcome": "completed"}}
            if method == "GET" and path == DIAGNOSTICS_PATH:
                return _diagnostics()
            if method == "POST" and path == BACKUPS_PATH:
                state["manual"] = True
                return {"jobId": "m1"}
            raise AssertionError((method, path))

        api = _FakeApi(handler)
        options = BackupWindowOptions(
            f"self-timing-{progress_until}-{fail_at}",
            scratch + f"/timing-{progress_until}-{fail_at}.json",
            scheduled_timeout=180,
            manual_timeout=10,
        )
        run = BackupWindowRun(
            api,
            options,
            clock=clock,
            sleep=clock.sleep,
            now=lambda: datetime.datetime(2026, 10, 28, 12, 0, tzinfo=datetime.timezone.utc),
        )
        return run.execute()

    # 60 s no-progress is accepted even when the whole operation is longer.
    long_total = scenario_busy_timing(progress_until=55, fail_at=116)
    assert long_total["result"] == "pass", long_total
    assert long_total["backups"]["scheduled"]["totalObservedMs"] >= 115_000
    assert 60_000 <= long_total["backups"]["scheduled"]["noProgressObservedMs"] <= 63_000
    results["no-progress-after-progress"] = long_total["result"]

    # 64 s without progress is outside the approved 60 s budget and must fail.
    late_busy = scenario_busy_timing(progress_until=0, fail_at=64)
    assert late_busy["result"] == "fail", late_busy
    results["late-busy-fails"] = late_busy["result"]

    results["busy-pass"] = scenario_busy()
    results["manual-refused"] = scenario_manual_refused()
    results["scheduled-timeout"] = scenario_scheduled_timeout()
    assert all(value in ("pass", "fail") for value in results.values())
    return results
