#!/usr/bin/env python3
"""Tiny mocked checks for the P2-65 operator tools. No socket, server, or product run."""

from __future__ import annotations

import datetime
import json
import re
import sys
from pathlib import Path

import appdata_copy
import backup_window
import common
import writer_wal


def source_key_check() -> dict:
    """Verify every diagnostics key against the Go JSON tags, when the source is present."""
    root = Path(__file__).resolve().parents[3]
    files = [
        root / "server/internal/dbwork/wal.go",
        root / "server/internal/dbwork/gate.go",
        root / "server/internal/dbwork/plan.go",
        root / "server/internal/dbwork/pressure.go",
        root / "server/internal/dbwork/health.go",
        root / "server/internal/dbwork/reader_tracking.go",
        root / "server/internal/dbwork/writer_discipline.go",
        root / "server/internal/httpapi/admin_concurrency.go",
    ]
    if not all(path.is_file() for path in files):
        return {"available": False, "reason": "server source not present beside this script"}
    text = "\n".join(path.read_text(encoding="utf-8") for path in files)

    def tags(name: str) -> set[str] | None:
        match = re.search(r"type " + re.escape(name) + r" struct \{(.*?)\n\}", text, re.S)
        if not match:
            return None
        return set(re.findall(r'`json:"([^",]+)', match.group(1)))

    expected = {
        "WALStats": {
            "walFileBytes", "walPeakFileBytes", "logFrames", "checkpointedFrames",
            "backlogFrames", "busy", "lastError", "readers", "checkpoints",
        },
        "ReaderLifetimeStats": {
            "snapshots", "oldestSnapshotMs", "maxSnapshotMs", "admissionWaiting",
            "admissionPaused", "admissionRefused",
        },
        "CheckpointStats": {"attempts", "truncates", "busy", "errors", "pressureTimeouts", "maxDurationMs"},
        "GateStats": {"active", "heldMillis", "maxHeldMillis", "queueWaitMillis", "byClass"},
        "GateClassStats": {"currentHeldMillis", "maxHeldMillis", "queued"},
        "AdmissionStats": {
            "capacity", "userQueued", "backgroundQueued", "deferred", "refused",
            "busyAfterAdmission", "recoveryHold", "recoveryPause",
        },
        "PlanStats": {"measured", "overruns", "overrunsByClass", "overrunsByDimension", "largest"},
        "PressureStats": {"yields", "foregroundActive", "healthy"},
        "HealthReport": {"state", "consecutiveFailures", "lastError", "handleRecycles", "backgroundPaused"},
        "ProcessDiagnostics": {"goroutines", "heapAllocBytes", "nextGCBytes", "gcCycles", "containedPanics"},
        "UngatedStats": {"writer", "reader"},
    }
    missing = {}
    for name, wanted in expected.items():
        actual = tags(name)
        if actual is None:
            missing[name] = "struct not found"
            continue
        absent = sorted(wanted - actual)
        if absent:
            missing[name] = absent
    backup_go = (root / "server/internal/backup/backup.go").read_text(encoding="utf-8")
    routes = (root / "server/internal/httpapi/backup_routes.go").read_text(encoding="utf-8")
    return {
        "available": True,
        "missing": missing,
        "backupBusyExact": '"backup_busy"' in backup_go,
        "routes": {
            "list": "GET\", Path: \"/v1/admin/backups\"" in routes,
            "start": "Path: \"/v1/admin/backups\"" in routes and "backup.KindManual" in routes,
            "restore": "Path: \"/v1/admin/backups/restore\"" in routes,
        },
        "diagnosticsRoute": "Path: \"/v1/admin/diagnostics/concurrency\"" in (
            root / "server/internal/httpapi/http_contracts_administration.go"
        ).read_text(encoding="utf-8"),
    }


def main() -> int:
    results: dict[str, object] = {}

    source = source_key_check()
    results["source-keys"] = source
    if source.get("available"):
        assert source["missing"] == {}, source
        assert source["backupBusyExact"] is True, source
        assert all(source["routes"].values()), source
        assert source["diagnosticsRoute"] is True, source

    schedule_settings, window = backup_window.build_schedule(
        backup_window._base_settings(),
        datetime.datetime(2026, 10, 28, 0, 0, tzinfo=datetime.timezone.utc),
        15,
    )
    assert window["timezone"] == "UTC"
    assert window["days"] == ["tuesday"]  # 23:58 UTC on 27 Oct is the start
    assert window["startMinute"] == 1438
    assert window["durationMinutes"] == 15
    assert schedule_settings["windows"][1]["id"] == "heavy"
    results["schedule-offset-midnight"] = "pass"

    over = common.summarize([common.compact_diagnostics(backup_window._diagnostics(peak=200 * 1024 * 1024))])
    checks = common.evaluate_thresholds(over, context="self-check")
    assert any(item["id"] == "wal.physicalPeakWithin192MiB" and item["status"] == "fail" for item in checks)
    results["wal-threshold"] = "pass"

    import os
    import tempfile

    scratch = tempfile.mkdtemp(prefix="p2-65-selftest-")
    try:
        plan = os.path.join(scratch, "plan.json")
        original = backup_window._base_settings()
        common.write_private_json(
            plan,
            {
                "kind": "p2-65-maintenance-restore-plan",
                "version": 1,
                "windowId": "self-restore",
                "originalSettings": original,
            },
        )
        changed = {**original, "windows": [dict(window) for window in original["windows"]]}
        changed["windows"][0]["startMinute"] = 1
        state = {"revision": 2, "settings": changed}

        class _RestoreApi:
            def request(self, method, path, body=None):
                if method == "GET" and path == common.MAINTENANCE_PATH:
                    return {"revision": state["revision"], "settings": state["settings"]}
                if method == "PUT" and path == common.MAINTENANCE_PATH:
                    assert body["expectedRevision"] == state["revision"]
                    state["revision"] += 1
                    state["settings"] = body["settings"]
                    return {"revision": state["revision"], "settings": state["settings"]}
                raise AssertionError((method, path))

        restored = backup_window.restore_only(_RestoreApi(), plan)
        assert restored["restored"] is True, restored
        assert not os.path.exists(plan)
        results["restore-only"] = "pass"
    finally:
        import shutil

        shutil.rmtree(scratch, ignore_errors=True)

    class _ManualOnlyApi:
        def __init__(self):
            self.calls = []

        def request(self, method, path, body=None):
            self.calls.append((method, path))
            if method == "GET" and path == common.MAINTENANCE_PATH:
                return {"revision": 1, "settings": backup_window._base_settings()}
            if method == "GET" and path == common.BACKUPS_PATH:
                if method == "POST":
                    raise AssertionError("unreachable")
                return {"backups": [], "lastBackup": {"jobId": "m1", "outcome": "completed"}}
            if method == "POST" and path == common.BACKUPS_PATH:
                return {"jobId": "m1"}
            if method == "GET" and path == common.DIAGNOSTICS_PATH:
                return backup_window._diagnostics()
            raise AssertionError((method, path))

    manual_api = _ManualOnlyApi()
    manual_receipt = backup_window.BackupWindowRun(
        manual_api,
        backup_window.BackupWindowOptions(
            "self-manual-only", os.path.join(scratch, "manual.json"), manual_only=True, manual_timeout=10
        ),
    ).execute()
    assert manual_receipt["result"] == "pass", manual_receipt
    assert manual_receipt["backups"]["scheduled"]["kindEvidence"] == "skipped"
    assert not any(method == "PUT" for method, _ in manual_api.calls)
    results["manual-only"] = "pass"

    busy = backup_window.self_check()
    assert busy["busy-pass"] == "pass"
    assert busy["manual-refused"] == "fail"
    assert busy["scheduled-timeout"] == "fail"
    results["backup-window"] = busy

    copy = appdata_copy.self_check()
    assert copy["copy-pass"] == "pass"
    assert copy["startup-fail"] == "pass"
    assert copy["nested-dest-refused"] == "pass"
    results["appdata-copy"] = copy

    writer = writer_wal.self_check()
    assert writer["inventory-complete"] == "pass"
    assert writer["inventory-failed"] == "fail"
    results["writer-wal"] = writer

    print(json.dumps({"selfCheck": "pass", "results": results}, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except AssertionError as error:
        print(json.dumps({"selfCheck": "fail", "detail": str(error)}), file=sys.stderr)
        raise SystemExit(1)
