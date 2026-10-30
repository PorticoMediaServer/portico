#!/usr/bin/env python3
"""P2-65 bounded source-inventory/quiet-gap writer/WAL observation. Operator-only; refused in CI."""

from __future__ import annotations

import argparse
import json
import sys

from common import ApiError, JsonApi, Refused, clean_absolute, read_token_file, refuse_in_ci, write_private_json
from writer_wal import WriterOptions, run, self_check


def options(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", help="Bare http(s) origin of the test server")
    parser.add_argument("--token-file", help="Operator-owned 0600 file with one owner bearer token")
    parser.add_argument("--library-id", help="Real library id to scan")
    parser.add_argument("--mode", choices=["incremental", "full", "integrity"], default="incremental")
    parser.add_argument("--observe-seconds", type=float, default=120.0)
    parser.add_argument("--poll-seconds", type=float, default=1.0)
    parser.add_argument("--stdout-log", help="Optional process log to search for the quiet-gap line")
    parser.add_argument("--out", help="New private receipt path (mode 0600)")
    parser.add_argument("--self-check", action="store_true")
    parser.add_argument("--execute", action="store_true")
    args = parser.parse_args(argv)
    if args.self_check:
        if args.execute or args.base or args.token_file:
            parser.error("--self-check takes no live input")
        return args
    if not args.execute:
        return args
    if not args.base or not args.token_file or not args.library_id or not args.out:
        parser.error("--execute needs --base, --token-file, --library-id and --out")
    if not 10 <= args.observe_seconds <= 1800:
        parser.error("--observe-seconds must be 10..1800")
    if not 0.5 <= args.poll_seconds <= 10:
        parser.error("--poll-seconds must be 0.5..10")
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
                        "sample the real owner diagnostics baseline",
                        "POST one real library scan with the named mode",
                        "poll the real ingestion job and sample diagnostics each second",
                        "search an optional process log for the internal quiet-gap line",
                        "apply the published WAL/hold/plan/refusal thresholds",
                    ],
                    "supported": ["real source inventory scan", "WAL physical/log-frame samples", "gate hold samples", "write-plan telemetry", "admission refusals"],
                    "unsupported": [
                        "agreed 300k real-scan scale bar (replaces the earlier 5M target)",
                        "removal/retention and SRV023 10k-track release",
                        "user-write progress under fan-out",
                        "internal WAL reservation ledger",
                        "quiet-gap deadline/state beyond a process-log line",
                    ],
                },
                indent=2,
            )
        )
        return 0

    refuse_in_ci(True)
    token = read_token_file(clean_absolute(args.token_file, must_exist=True))
    api = JsonApi(args.base, token)
    try:
        receipt = run(
            api,
            WriterOptions(
                library_id=args.library_id,
                mode=args.mode,
                observe_seconds=args.observe_seconds,
                poll_seconds=args.poll_seconds,
                stdout_log=args.stdout_log,
                out=args.out,
            ),
        )
    except ApiError as error:
        print(json.dumps({"apiError": {"status": error.status, "code": error.code}}), file=sys.stderr)
        return 1
    write_private_json(args.out, receipt)
    print(json.dumps({"result": receipt.get("result"), "receipt": str(clean_absolute(args.out))}))
    return 0 if receipt.get("result") == "pass" else 1


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Refused as error:
        print(json.dumps({"refused": str(error)}), file=sys.stderr)
        raise SystemExit(2)
