#!/usr/bin/env python3
"""Reduce optional syscall attribution for GV keeper component records.

Build with Go1.26.10, then run the disposable test binary under:
 strace -f -ttt -T -yy -s 0 -e trace=pwrite64,fsync,fdatasync,ftruncate,unlink,unlinkat -o trace.txt <binary> -test.v -test.run '^TestDiagnosticKeeperDiscard/64MiB/keeper$'
 python3 ci/checkpoint_trace.py --records component.log --trace trace.txt

Accepts plain test output or fragmented go-test JSON. Syscall time is an observed
window envelope, not a VFS ownership oracle. Ptrace overhead is NOT subtracted.
Unmatched/incomplete trace lines and partial overlaps remain explicit.
"""
import argparse
import datetime
import decimal
import json
import re
from pathlib import Path

MARKER = "GV_KEEPER_CONCURRENT_V1 "


def records(text):
    outputs = {}
    plain = []
    for line in text.splitlines(keepends=True):
        try:
            event = json.loads(line)
        except (ValueError, TypeError):
            plain.append(line)
            continue
        if isinstance(event, dict) and isinstance(event.get("Output"), str):
            key = (event.get("Package"), event.get("Test"))
            outputs.setdefault(key, []).append(event["Output"])
    streams = ["".join(plain)] + ["".join(parts) for parts in outputs.values()]
    for stream in streams:
        for line in stream.splitlines():
            if MARKER in line:
                yield json.loads(line.split(MARKER, 1)[1])


def epoch_ns(iso):
    m = re.fullmatch(r"(.{19})(?:\.(\d{1,9}))?(Z|[+-]\d\d:\d\d)", iso)
    if not m:
        raise ValueError("invalid timestamp: " + iso)
    dt = datetime.datetime.fromisoformat(m[1] + m[3].replace("Z", "+00:00"))
    return int(dt.timestamp()) * 10**9 + int((m[2] or "").ljust(9, "0"))


def seconds_ns(value):
    return int(decimal.Decimal(value) * 10**9)


def trace_calls(text):
    calls, pending = [], {}
    incomplete = 0
    for raw in text.splitlines():
        line = re.match(r"^(?:\[pid\s+(\d+)\]\s+|(\d+)\s+)?(\d+\.\d+)\s+(.*)$", raw)
        if not line:
            continue  # signal/exit and non-syscall control lines
        tid, start, body = line[1] or line[2] or "single", seconds_ns(line[3]), line[4]
        if "<unfinished ...>" in body:
            if tid in pending:
                incomplete += 1
            pending[tid] = (start, body.replace("<unfinished ...>", ""))
            continue
        if body.startswith("<... "):
            old = pending.pop(tid, None)
            resumed = re.match(r"<\.\.\. \w+ resumed>(.*)", body)
            if old is None or resumed is None:
                incomplete += 1
                continue
            start, prefix = old
            body = prefix + resumed[1]
        call = re.match(r"(pwrite64|fsync|fdatasync|ftruncate|unlink|unlinkat)\((.*)\)\s+=\s+(-?\d+).*<(\d+\.\d+)>$", body)
        if not call:
            if re.match(r"(?:pwrite64|fsync|fdatasync|ftruncate|unlink|unlinkat)\(", body):
                incomplete += 1
            continue
        elapsed = seconds_ns(call[4])
        paths = re.findall(r"\d+<([^>]+)>", call[2])
        calls.append(dict(name=call[1], start=start, end=start + elapsed,
                          duration=elapsed, result=int(call[3]), paths=paths))
    return calls, incomplete + len(pending)


def window(calls, path, start, end):
    totals = {kind: dict(contained=0, partial=0, duration_ns=0, write_bytes=0, errors=0)
              for kind in ("database", "wal", "directory")}
    directory = str(Path(path).parent)
    for call in calls:
        kind = None
        for observed in call["paths"]:
            observed = observed.removesuffix(" (deleted)")
            if observed == path:
                kind = "database"
            elif observed == path + "-wal":
                kind = "wal"
            elif observed == directory:
                kind = "directory"
        if kind is None or call["end"] <= start or call["start"] >= end:
            continue
        item = totals[kind]
        if call["start"] < start or call["end"] > end:
            item["partial"] += 1
            continue
        item["contained"] += 1
        item["duration_ns"] += call["duration"]
        if call["result"] < 0:
            item["errors"] += 1
        elif call["name"] == "pwrite64":
            item["write_bytes"] += call["result"]
        if call["name"] in ("fsync", "fdatasync"):
            item["sync_count"] = item.get("sync_count", 0) + 1
            item["sync_ns"] = item.get("sync_ns", 0) + call["duration"]
    return totals


def reduce(record, calls, incomplete):
    path = record["DatabasePath"]
    origin = epoch_ns(record["OriginUTC"])
    output = dict(cell=record["Cell"], kind=record["Kind"], keeper=record.get("Keeper"),
                  qualification=False, observer="strace/ptrace; overhead included",
                  incomplete_trace_lines=incomplete, windows=[])
    for close in record.get("Writer", {}).get("Closes") or []:
        if close.get("started_ns") is None or close.get("returned_ns") is None:
            output["windows"].append(dict(kind="native_close", unavailable="physical interval incomplete"))
            continue
        a, b = origin + close["started_ns"], origin + close["returned_ns"]
        output["windows"].append(dict(kind="native_close", connection=close["connection_id"],
                                      elapsed_ns=b-a, syscall_envelope=window(calls, path, a, b)))
    cp = record.get("Checkpoint")
    if cp and cp.get("NativeStarted") and not cp["NativeStarted"].startswith("0001") and not cp["NativeEnded"].startswith("0001"):
        a, b = epoch_ns(cp["NativeStarted"]), epoch_ns(cp["NativeEnded"])
        output["windows"].append(dict(kind="checkpoint_native", elapsed_ns=b-a,
                                      syscall_envelope=window(calls, path, a, b),
                                      scope="path-and-time envelope; concurrent W syscalls may overlap"))
    return output


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--records", type=Path, required=True)
    parser.add_argument("--trace", type=Path, required=True)
    args = parser.parse_args()
    calls, incomplete = trace_calls(args.trace.read_text())
    found = list(records(args.records.read_text()))
    if not found:
        raise SystemExit("no complete component records; preserve failed raw input")
    print(json.dumps([reduce(r, calls, incomplete) for r in found], indent=2))


if __name__ == "__main__":
    main()
