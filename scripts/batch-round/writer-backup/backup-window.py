#!/usr/bin/env python3
"""P2-65 scheduled/manual backup orchestration. Operator-only; refused in CI."""

from __future__ import annotations

import argparse
import datetime
import json
import re
import sys

from backup_window import BackupWindowOptions, BackupWindowRun, restore_only, self_check
from common import ApiError, JsonApi, Refused, clean_absolute, read_token_file, refuse_in_ci, write_private_json


def options(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", help="Bare http(s) origin of the test server")
    parser.add_argument("--token-file", help="Operator-owned 0600 file with one owner bearer token")
    parser.add_argument("--out", help="New private receipt path (mode 0600)")
    parser.add_argument("--window-id", help="Operator window id shared with load.ts --operator-window")
    parser.add_argument("--window-minutes", type=int, default=45)
    parser.add_argument("--scheduled-timeout", type=float, default=360.0)
    parser.add_argument("--manual-timeout", type=float, default=600.0)
    parser.add_argument("--poll-seconds", type=float, default=1.0)
    parser.add_argument("--keep-count", type=int, help="Optionally set and restore backupKeepCount")
    parser.add_argument("--load-receipt", help="Existing load.ts JSON receipt to correlate")
    parser.add_argument("--restore-plan", help="Restore-plan path; defaults beside --out")
    parser.add_argument("--manual-only", action="store_true", help="Run only the manual backup under a separate load label")
    parser.add_argument("--restore-only", action="store_true", help="Restore the original settings from a plan")
    parser.add_argument("--confirm", help="Literal: CHANGE-MAINTENANCE-WINDOW")
    parser.add_argument("--self-check", action="store_true")
    parser.add_argument("--execute", action="store_true")
    args = parser.parse_args(argv)

    if args.self_check:
        if args.execute or args.base or args.token_file or args.out:
            parser.error("--self-check takes no live input")
        return args
    if not args.execute:
        if args.restore_only:
            parser.error("--restore-only requires --execute")
        return args
    if args.restore_only:
        if not args.base or not args.token_file or not args.restore_plan:
            parser.error("--restore-only needs --base, --token-file and --restore-plan")
        return args
    if not args.base or not args.token_file or not args.out:
        parser.error("--execute needs --base, --token-file and --out")
    if not args.manual_only and args.confirm != "CHANGE-MAINTENANCE-WINDOW":
        parser.error("--execute needs --confirm CHANGE-MAINTENANCE-WINDOW (or --manual-only)")
    if not 15 <= args.window_minutes <= 240:
        parser.error("--window-minutes must be 15..240")
    if not 120 <= args.scheduled_timeout <= 900:
        parser.error("--scheduled-timeout must be 120..900 seconds")
    if not 60 <= args.manual_timeout <= 3600:
        parser.error("--manual-timeout must be 60..3600 seconds")
    if not 0.5 <= args.poll_seconds <= 10:
        parser.error("--poll-seconds must be 0.5..10")
    if args.keep_count is not None and not 1 <= args.keep_count <= 365:
        parser.error("--keep-count must be 1..365")
    if args.manual_only and args.keep_count is not None:
        parser.error("--manual-only does not change backupKeepCount; drop --keep-count")
    if args.window_id and not re.fullmatch(r"[A-Za-z0-9_.:-]{1,80}", args.window_id):
        parser.error("--window-id must be 1..80 safe characters")
    return args


def main(argv: list[str] | None = None) -> int:
    args = options(sys.argv[1:] if argv is None else argv)
    if args.self_check:
        print(json.dumps({"mode": "self-check", "results": self_check()}, sort_keys=True))
        return 0
    if not args.execute:
        print(
            json.dumps(
                {
                    "state": "UNRUN",
                    "mode": "plan",
                    "steps": [
                        "read the real maintenance document and backups document",
                        "write a private restore plan, then open the nightly backup window now",
                        "wait for scheduled-kind evidence in running or a new listed scheduled backup",
                        "accept success or the documented ~60 s backup_busy outcome",
                        "start one manual backup through POST /v1/admin/backups and reject capacity refusals",
                        "sample GET /v1/admin/diagnostics/concurrency for WAL/holds/plans/pressure",
                        "restore the original maintenance settings in finally",
                    ],
                    "thresholds": {
                        "walPhysicalPeakBytes": 192 * 1024 * 1024,
                        "holdFindingMillis": 250,
                        "scheduledNoProgressBudgetMillis": 60_000,
                        "scheduledNoProgressTolerance": "one measured sampler interval plus 2 s; never a wider product gate",
                    },
                    "unsupported": [
                        "wal reservation ledger (internal dbwork.WALReservations)",
                        "per-occurrence >250 ms writer log (SRV023/P2-52b path)",
                        "per-collector queue diagnostics",
                        "user write latency without the mixed-client load driver",
                    ],
                },
                indent=2,
            )
        )
        return 0

    refuse_in_ci(True)
    token = read_token_file(clean_absolute(args.token_file, must_exist=True))
    api = JsonApi(args.base, token)
    if args.restore_only:
        result = restore_only(api, args.restore_plan)
        print(json.dumps(result, sort_keys=True))
        return 0 if result.get("restored") else 1

    window_id = args.window_id or "p2-65-" + datetime.datetime.now(
        datetime.timezone.utc
    ).strftime("%Y%m%dT%H%M%SZ")
    run = BackupWindowRun(
        api,
        BackupWindowOptions(
            window_id=window_id,
            out=args.out,
            window_minutes=args.window_minutes,
            scheduled_timeout=args.scheduled_timeout,
            manual_timeout=args.manual_timeout,
            poll_seconds=args.poll_seconds,
            keep_count=args.keep_count,
            load_receipt=args.load_receipt,
            restore_plan=args.restore_plan,
            manual_only=args.manual_only,
        ),
    )
    receipt = run.execute()
    write_private_json(args.out, receipt)
    print(json.dumps({"result": receipt.get("result"), "windowId": window_id, "receipt": str(clean_absolute(args.out))}))
    return 0 if receipt.get("result") == "pass" else 1


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Refused as error:
        print(json.dumps({"refused": str(error)}), file=sys.stderr)
        raise SystemExit(2)
    except ApiError as error:
        print(json.dumps({"apiError": {"status": error.status, "code": error.code}}), file=sys.stderr)
        raise SystemExit(1)
