#!/usr/bin/env python3
"""100 ms host/cgroup/statfs/RSS/session sampler for one G07 phase.

Reads kernel facts and the owner diagnostics route only. It never reads server
command lines, environments, database contents or media paths. Output is a
private 0600 JSONL file; the bearer token stays in its 0600 state file.
"""

import argparse
import json
import os
import subprocess
import sys
import time

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "lib"))
import api  # noqa: E402

CGROUP_ROOT = "/sys/fs/cgroup"


def read_text(path):
    try:
        with open(path, "r", encoding="utf-8") as handle:
            return handle.read().strip()
    except OSError:
        return None


def int_or_none(value):
    try:
        return int(value)
    except (TypeError, ValueError):
        return None


def cgroup_samples(pid):
    raw = read_text("/proc/%d/cgroup" % pid)
    if not raw:
        return []
    relative = None
    for line in raw.splitlines():
        parts = line.split(":", 2)
        if len(parts) == 3 and parts[0] == "0" and parts[1] == "":
            relative = parts[2].lstrip("/")
            break
    if relative is None:
        return []
    chain = []
    current = CGROUP_ROOT
    for part in [p for p in relative.split("/") if p]:
        current = os.path.join(current, part)
        chain.append(current)
    chain.append(CGROUP_ROOT)
    out = []
    for path in chain:
        if not os.path.isdir(path):
            continue
        events = {}
        for line in (read_text(os.path.join(path, "memory.events")) or "").splitlines():
            key, _, value = line.partition(" ")
            events[key] = int_or_none(value)
        out.append({
            "path": path,
            "current": int_or_none(read_text(os.path.join(path, "memory.current"))),
            "max": read_text(os.path.join(path, "memory.max")),
            "high": read_text(os.path.join(path, "memory.high")),
            "events": events,
        })
    return out


def process_tree(pid):
    procs = {}
    for entry in os.listdir("/proc"):
        if not entry.isdigit():
            continue
        raw = read_text("/proc/%s/stat" % entry)
        if not raw or ")" not in raw:
            continue
        after = raw.rsplit(")", 1)[1].split()
        # After comm: state ppid ... starttime is field 22 overall, 20 here.
        try:
            ppid = int(after[1])
            start = int(after[19])
        except (IndexError, ValueError):
            continue
        rss = None
        statm = read_text("/proc/%s/statm" % entry)
        if statm:
            fields = statm.split()
            if len(fields) > 1:
                rss = int_or_none(fields[1])
        procs[int(entry)] = {"ppid": ppid, "start": start, "rssPages": rss}
    roots = [pid]
    children = []
    while roots:
        parent = roots.pop()
        for child, facts in procs.items():
            if facts["ppid"] == parent and child not in children:
                children.append(child)
                roots.append(child)
    return procs.get(pid), children, procs


def statfs_ns(pid, container_path):
    """statfs a path in the container mount namespace, not on the host."""
    try:
        proc = subprocess.run(
            ["nsenter", "-t", str(pid), "-m", "--", "stat", "-f", "-c", "%b %f %a %S %c %d", container_path],
            check=True, capture_output=True, text=True)
    except (OSError, subprocess.CalledProcessError):
        return None
    fields = proc.stdout.split()
    if len(fields) < 4:
        return None
    blocks, free, available, bsize = (int(x) for x in fields[:4])
    files = int(fields[4]) if len(fields) > 4 and fields[4].isdigit() else 0
    files_free = int(fields[5]) if len(fields) > 5 and fields[5].isdigit() else 0
    return {"blockSize": bsize, "blocks": blocks, "blocksFree": free,
            "blocksAvailable": available, "files": files, "filesFree": files_free,
            "totalBytes": blocks * bsize, "freeBytes": free * bsize,
            "availableBytes": available * bsize,
            "usedBytes": (blocks - free) * bsize}


def directory_bytes_ns(pid, container_path):
    try:
        proc = subprocess.run(
            ["nsenter", "-t", str(pid), "-m", "--", "du", "-sb", container_path],
            check=True, capture_output=True, text=True)
        parts = proc.stdout.split()
        return int(parts[0]), None
    except (OSError, subprocess.CalledProcessError, ValueError, IndexError):
        return None, None


def meminfo():
    out = {}
    for line in (read_text("/proc/meminfo") or "").splitlines():
        key, _, rest = line.partition(":")
        if key in ("MemTotal", "MemAvailable", "MemFree", "SwapTotal", "SwapFree"):
            out[key] = int_or_none(rest.strip().split()[0])
    for key in list(out):
        value = out.get(key)
        if value is not None:
            out[key + "Bytes"] = value * 1024
    return out


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--pid", type=int, required=True)
    parser.add_argument("--memory-dir")
    parser.add_argument("--disk-dir")
    parser.add_argument("--base", required=True)
    parser.add_argument("--token-file", required=True)
    parser.add_argument("--session-ids", default="")
    parser.add_argument("--interval", type=float, default=0.1)
    parser.add_argument("--seconds", type=float, default=1800)
    parser.add_argument("--api-every", type=int, default=10)
    parser.add_argument("--disk-every", type=int, default=10)
    parser.add_argument("--stop-file")
    parser.add_argument("--out", required=True)
    parser.add_argument("--execute", action="store_true")
    args = parser.parse_args()
    if not args.execute:
        print("PLAN: would sample pid %d every %.0f ms" % (args.pid, args.interval * 1000))
        return 0
    if args.interval < 0.05:
        raise SystemExit("interval below 50 ms is not supported")
    sessions = [s for s in args.session_ids.split(",") if s]
    client = api.Api.load(args.base, args.token_file)

    fd = os.open(args.out, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    start = time.time()
    initial = None
    with os.fdopen(fd, "w", encoding="utf-8") as out:
        index = 0
        while time.time() - start < args.seconds:
            if args.stop_file and os.path.exists(args.stop_file):
                break
            facts, children, procs = process_tree(args.pid)
            if facts is None:
                out.write(json.dumps({"t": time.time(), "pidGone": True}) + "\n")
                return 3
            if initial is None:
                initial = facts["start"]
            sample = {
                "t": time.time(),
                "index": index,
                "pid": args.pid,
                "pidAlive": facts["start"] == initial,
                "server": {"rssBytes": (facts["rssPages"] or 0) * os.sysconf("SC_PAGE_SIZE")},
                "children": {
                    "count": len(children),
                    "rssBytes": sum((procs[c]["rssPages"] or 0) for c in children) * os.sysconf("SC_PAGE_SIZE"),
                },
                "host": meminfo(),
                "cgroup": cgroup_samples(args.pid),
            }
            if args.memory_dir:
                sample["memoryDir"] = statfs_ns(args.pid, args.memory_dir)
            if args.disk_dir and index % args.disk_every == 0:
                used, _ = directory_bytes_ns(args.pid, args.disk_dir)
                sample["diskDir"] = {"bytes": used, **(statfs_ns(args.pid, args.disk_dir) or {})}
            if client is not None and index % args.api_every == 0:
                try:
                    sample["diagnostics"] = api.diagnostics(client)
                    sample["sessions"] = [
                        {k: v for k, v in api.get_session(client, sid).items()
                         if k in ("id", "state", "revision", "presentation", "end")}
                        for sid in sessions
                    ]
                except api.ApiError as err:
                    sample["apiError"] = {"status": err.status, "code": err.code}
            out.write(json.dumps(sample) + "\n")
            index += 1
            time.sleep(args.interval)
    print("sampled %d points into a private 0600 file" % index)
    return 0


if __name__ == "__main__":
    sys.exit(main())
