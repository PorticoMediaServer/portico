#!/usr/bin/env python3
"""P2-65 separate-destination live/stopped appdata-copy recovery. Operator-only; refused in CI."""

from __future__ import annotations

import argparse
import json
import sys

from appdata_copy import CopyOptions, run, self_check
from common import Refused, clean_absolute, read_env_file, refuse_in_ci, write_private_json

FORBIDDEN_ENV = {"PORTICO_DATA_DIR", "PORTICO_BIND", "PORTICO_DISCOVERY", "PORTICO_SERVICE", "PORTICO_CONTAINER"}


def options(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-state", help="Existing source appdata state directory")
    parser.add_argument("--dest-state", help="Separate destination; must be absent or empty")
    parser.add_argument("--mode", choices=["live", "stopped"])
    parser.add_argument("--scope", choices=["recovery", "full"], default="recovery")
    parser.add_argument("--binary", help="Current server binary to start on the copy")
    parser.add_argument("--bind", help="Scratch listener, host:port (use a free loopback port)")
    parser.add_argument("--scheme", choices=["http", "https"], default="http")
    parser.add_argument("--out", help="New private receipt path (mode 0600)")
    parser.add_argument("--env-file", help="Optional operator-owned 0600 KEY=VALUE file for the scratch server")
    parser.add_argument("--env", action="append", default=[], metavar="KEY=VALUE")
    parser.add_argument("--ready-timeout", type=float, default=180.0)
    parser.add_argument("--stop-timeout", type=float, default=60.0)
    parser.add_argument("--integrity", choices=["none", "quick", "full"], default="quick")
    parser.add_argument("--max-bytes", type=int, default=50 * 1024**3)
    parser.add_argument("--retain", action="store_true", help="Keep the separate copy and scratch home")
    parser.add_argument("--hash", action="store_true", help="Hash every copied file (slow on a large state)")
    parser.add_argument("--log", help="Scratch server stdout/stderr path (defaults beside --out)")
    parser.add_argument("--self-check", action="store_true")
    parser.add_argument("--execute", action="store_true")
    args = parser.parse_args(argv)
    if args.self_check:
        if args.execute or args.source_state or args.dest_state:
            parser.error("--self-check takes no live input")
        return args
    if not args.execute:
        return args
    for name in ("source_state", "dest_state", "mode", "binary", "bind", "out"):
        if not getattr(args, name):
            parser.error(f"--execute needs --{name.replace('_', '-')}")
    if not 10 <= args.ready_timeout <= 900:
        parser.error("--ready-timeout must be 10..900 seconds")
    if not 5 <= args.stop_timeout <= 300:
        parser.error("--stop-timeout must be 5..300 seconds")
    if not 1024 * 1024 <= args.max_bytes <= 1024**4:
        parser.error("--max-bytes must be between 1 MiB and 1 TiB")
    return args


def merged_env(args: argparse.Namespace) -> dict[str, str]:
    values: dict[str, str] = {}
    if args.env_file:
        values.update(read_env_file(clean_absolute(args.env_file, must_exist=True)))
    for pair in args.env:
        if "=" not in pair:
            raise Refused("--env must be KEY=VALUE")
        key, value = pair.split("=", 1)
        values[key] = value
    for key in values:
        if key in FORBIDDEN_ENV:
            raise Refused(f"{key} is controlled by this tool and cannot be overridden")
    return values


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
                        "refuse a destination inside the source and any symlinked path",
                        "copy the selected state scope to a separate destination, preserving WAL/SHM",
                        "record pre-start WAL/SHM sizes",
                        "start the current server binary on the copy with PORTICO_DATA_DIR=destination",
                        "wait for unauthenticated /v1/readiness",
                        "stop the scratch server and record clean/forced",
                        "inspect the copied database read-only for state_directory, no pending rebase, and integrity",
                        "remove the copy unless --retain",
                    ],
                    "caveat": "a live multi-file copy is not an atomic snapshot; use the product backup for a consistent live copy",
                    "scopes": {
                        "recovery": "Database (main+WAL+SHM), Keys, portico.toml, ownership markers",
                        "full": "every regular file in the state directory; symlinks refused, non-regular files skipped",
                    },
                },
                indent=2,
            )
        )
        return 0

    refuse_in_ci(True)
    options_value = CopyOptions(
        source_state=args.source_state,
        dest_state=args.dest_state,
        mode=args.mode,
        scope=args.scope,
        binary=args.binary,
        bind=args.bind,
        out=args.out,
        scheme=args.scheme,
        env=merged_env(args),
        ready_timeout=args.ready_timeout,
        stop_timeout=args.stop_timeout,
        integrity=args.integrity,
        max_bytes=args.max_bytes,
        retain=args.retain,
        hash_files=args.hash,
        log_path=args.log,
    )
    receipt = run(options_value)
    write_private_json(args.out, receipt)
    print(json.dumps({"result": receipt.get("result"), "receipt": str(clean_absolute(args.out))}))
    return 0 if receipt.get("result") == "pass" else 1


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Refused as error:
        print(json.dumps({"refused": str(error)}), file=sys.stderr)
        raise SystemExit(2)
