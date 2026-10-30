#!/usr/bin/env python3
"""Evaluate G07 receipts against the named batch-round gates.

A gate that cannot be evaluated (missing samples, a skipped phase, a namespace
skip) is recorded as `blocked`, never as a pass. Exit code 1 if any gate failed,
2 if any gate is blocked and none failed.
"""

import argparse
import hashlib
import json
import os
import sys

GIB = 1024 ** 3


def load_jsonl(path):
    out = []
    with open(path, "r", encoding="utf-8") as handle:
        for line in handle:
            line = line.strip()
            if line:
                out.append(json.loads(line))
    return out


def size_bytes(value):
    if value in (None, "", "max"):
        return None
    text = str(value).strip().lower()
    units = {"k": 1024, "m": 1024 ** 2, "g": 1024 ** 3}
    if text and text[-1] in units:
        try:
            return int(float(text[:-1]) * units[text[-1]])
        except ValueError:
            return None
    try:
        return int(text)
    except ValueError:
        return None


def cgroup_limit(samples):
    limits = []
    for sample in samples:
        for entry in sample.get("cgroup", []):
            value = size_bytes(entry.get("max"))
            if value:
                limits.append(value)
    return min(limits) if limits else None


def oom_delta(samples):
    first, last = {}, {}
    for sample in samples:
        for entry in sample.get("cgroup", []):
            events = entry.get("events") or {}
            for key in ("oom", "oom_kill"):
                value = events.get(key)
                if value is None:
                    continue
                first.setdefault((entry["path"], key), value)
                last[(entry["path"], key)] = value
    return {k: last[k] - first.get(k, 0) for k in last}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--samples", required=True)
    parser.add_argument("--drive", required=True)
    parser.add_argument("--container", required=True)
    parser.add_argument("--phase", required=True)
    parser.add_argument("--sentinel")
    parser.add_argument("--sentinel-sha")
    parser.add_argument("--restart")
    parser.add_argument("--allow-restart", action="store_true")
    parser.add_argument("--expected-tmpfs-mib", type=int, default=0)
    parser.add_argument("--out", required=True)
    args = parser.parse_args()

    samples = load_jsonl(args.samples)
    drive = json.load(open(args.drive, "r", encoding="utf-8"))
    container = json.load(open(args.container, "r", encoding="utf-8"))
    gates = []

    def gate(name, passed, detail, blocked=False):
        gates.append({"name": name, "passed": bool(passed), "blocked": bool(blocked), "detail": detail})

    if not samples:
        gate("samples-present", False, "no samples", blocked=True)
    else:
        gate("samples-present", True, "%d samples" % len(samples))

    memory_phases = {"memory-2g", "memory-4g", "memory-8g", "parent-2g", "spill-fail", "restart-after-spill"}
    pressure = args.phase in memory_phases
    floor = args.phase == "floor-1g"
    disk = args.phase == "disk-1g"

    alive = [s for s in samples if s.get("pidAlive")]
    expected_gone = drive.get("interrupted") or args.allow_restart
    gate("server-stayed-up", len(alive) == len(samples) or expected_gone,
         "%d of %d samples alive" % (len(alive), len(samples)))

    oom = oom_delta(samples)
    gate("no-oom-event", not any(v > 0 for v in oom.values()), {"oomDeltas": oom})

    limit = cgroup_limit(samples)
    reserve = max(GIB, int(limit * 0.10)) if limit else GIB
    available = [s.get("host", {}).get("MemAvailableBytes") for s in samples
                 if s.get("host", {}).get("MemAvailableBytes")]
    if available:
        gate("host-reserve", min(available) >= reserve,
             {"minMemAvailableBytes": min(available), "requiredBytes": reserve})
    else:
        gate("host-reserve", False, "no MemAvailable samples", blocked=True)

    memory_used = [s["memoryDir"]["usedBytes"] for s in samples if s.get("memoryDir")]
    memory_total = [s["memoryDir"]["totalBytes"] for s in samples if s.get("memoryDir")]
    if memory_total:
        ceiling_ok = all(u <= t for u, t in zip(memory_used, memory_total))
        size_ok = True
        if args.expected_tmpfs_mib:
            want = args.expected_tmpfs_mib * 1024 ** 2
            size_ok = all(abs(t - want) <= want * 0.02 for t in memory_total)
        gate("tmpfs-ceiling", ceiling_ok and size_ok,
             {"maxUsedBytes": max(memory_used), "totalBytes": memory_total[0], "sizeMatches": size_ok})
        if limit:
            gate("tmpfs-within-admitted-cap", max(memory_total) <= limit,
                 {"tmpfsBytes": max(memory_total), "effectiveCapBytes": limit})
    elif pressure:
        gate("tmpfs-ceiling", False, "phase needs tmpfs samples", blocked=True)

    rss_peak = 0
    for sample in samples:
        rss = (sample.get("server", {}).get("rssBytes") or 0) + (sample.get("children", {}).get("rssBytes") or 0)
        rss_peak = max(rss_peak, rss)
    if limit:
        gate("rss-below-cgroup", rss_peak < limit, {"peakRssBytes": rss_peak, "limitBytes": limit})

    diags = [s.get("diagnostics") for s in samples if s.get("diagnostics")]
    max_sessions = max((d.get("activeConversionSessions") or 0) for d in diags) if diags else 0
    max_spills = max((d.get("memorySpills") or 0) for d in diags) if diags else 0
    max_refused = max((d.get("memoryPlacementsRefused") or 0) for d in diags) if diags else 0
    max_conversions_refused = max((d.get("conversionsRefused") or 0) for d in diags) if diags else 0
    session_count = len(drive.get("sessions", []))
    if pressure:
        gate("conversions-active", max_sessions >= 2, {"maxActiveConversionSessions": max_sessions})
        gate("no-conversion-refusal", max_conversions_refused == 0, {"conversionsRefused": max_conversions_refused})
        gate("one-spill-per-session", 1 <= max_spills <= session_count,
             {"memorySpills": max_spills, "sessions": session_count})
    if floor:
        gate("floor-refuses-ram", max_refused >= 1, {"memoryPlacementsRefused": max_refused})
        playing = [s for s in drive.get("sessions", []) if s.get("initialSegment")]
        gate("floor-still-plays-from-disk", bool(playing) and len(playing) == len(drive.get("sessions", [])),
             {"sessionsWithSegments": len(playing), "sessions": len(drive.get("sessions", []))})
    if disk:
        gate("disk-progress", any(s.get("diskDir", {}).get("bytes", 0) > 0 for s in samples),
             "no disk bytes recorded", blocked=not any("diskDir" in s for s in samples))

    matched = 0
    mismatched = 0
    for session in drive.get("sessions", []):
        initial, final = session.get("initialSegment"), session.get("finalSegment")
        if not initial or not final:
            continue
        if initial.get("generation") != final.get("generation"):
            continue
        if initial.get("sha256") == final.get("sha256") and initial.get("size") == final.get("size"):
            matched += 1
        else:
            mismatched += 1
    if pressure or disk:
        gate("generation-bytes-match", matched >= 1 and mismatched == 0,
             {"matched": matched, "mismatched": mismatched},
             blocked=matched == 0 and mismatched == 0)

    if args.phase == "restart-after-spill":
        if not args.restart:
            gate("restart-branch", False, "no restart receipt", blocked=True)
        else:
            branch = json.load(open(args.restart, "r", encoding="utf-8"))
            survived = branch.get("branch") == "session-survived"
            followup = branch.get("branch") == "session-ended-disk-followup"
            ok = (survived and matched >= 1) or (followup and bool(branch.get("followupDiskBytes")))
            gate("restart-branch", ok, branch)
            last_spills = (diags[-1].get("memorySpills") or 0) if diags else 0
            gate("restart-no-loop-into-ram",
                 branch.get("spillsAfterRestart") is not None
                 and last_spills == branch.get("spillsAfterRestart"),
                 {"spillsAfterRestart": branch.get("spillsAfterRestart"),
                  "spillsAtLastSample": last_spills})

    spill_at = drive.get("spillObservedAt")
    if pressure and spill_at is not None:
        after = [s["diskDir"]["bytes"] for s in samples
                 if "diskDir" in s and s.get("t", 0) >= spill_at]
        before = [s["diskDir"]["bytes"] for s in samples
                  if "diskDir" in s and s.get("t", 0) < spill_at]
        ok = bool(after) and (not before or max(after) > min(before))
        gate("disk-progress-after-spill", ok,
             {"beforeBytes": max(before) if before else None, "afterBytes": max(after) if after else None})
    elif pressure and args.phase == "spill-fail":
        gate("disk-progress-after-spill", any(
            s.get("diskDir", {}).get("bytes", 0) > 0 for s in samples),
            "spill-fail phase requires disk output", blocked=False)
    else:
        gate("disk-progress-after-spill", True, "not applicable")

    if args.sentinel:
        if not os.path.isfile(args.sentinel):
            gate("unowned-sentinel-preserved", False, "sentinel missing")
        else:
            digest = hashlib.sha256(open(args.sentinel, "rb").read()).hexdigest()
            if args.sentinel_sha:
                gate("unowned-sentinel-preserved", digest == args.sentinel_sha,
                     {"sha256": digest, "expected": args.sentinel_sha})
            else:
                gate("unowned-sentinel-preserved", False, "sentinel digest not supplied", blocked=True)
    else:
        gate("unowned-sentinel-preserved", False, "no sentinel supplied", blocked=True)

    errors = drive.get("errors", [])
    session_errors = [e for s in drive.get("sessions", []) for e in s.get("errors", [])]
    gate("no-driver-errors", not errors and not session_errors,
         {"driverErrors": errors, "sessionErrors": session_errors[:10]})

    failed = [g for g in gates if not g["passed"] and not g["blocked"]]
    blocked = [g for g in gates if g["blocked"]]
    receipt = {"phase": args.phase, "container": container, "gates": gates,
               "failed": len(failed), "blocked": len(blocked),
               "result": "FAIL" if failed else ("BLOCKED-ENV" if blocked else "PASS")}
    fd = os.open(args.out, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    with os.fdopen(fd, "w", encoding="utf-8") as handle:
        json.dump(receipt, handle, indent=2)
        handle.write("\n")
    for entry in gates:
        state = "PASS" if entry["passed"] else ("BLOCKED" if entry["blocked"] else "FAIL")
        print("%-7s %s" % (state, entry["name"]))
    print(receipt["result"])
    return 0 if not failed and not blocked else (1 if failed else 2)


if __name__ == "__main__":
    sys.exit(main())
