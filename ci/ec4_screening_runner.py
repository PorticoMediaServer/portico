#!/usr/bin/env python3
"""Run one EC4 flat-inventory SCREENING cell from a pushed source checkout.

This is disposable evidence machinery, not a qualification or release gate.
It compiles the writer test binary from an arbitrary pushed ref, then runs
exactly one test (`TestEC4NativeScreening`) against a new, task-owned flat
library root. The SSD cell is unthrottled; the `throttled-hdd` cell runs the
whole test binary inside a transient systemd unit whose read bandwidth/IOPS
limits apply to the media block device.

The runner never mounts, remounts, drops caches or changes host-wide limits.
It only creates and removes its own stamp-scoped scratch directories.
"""

from __future__ import annotations

import argparse
import datetime
import hashlib
import json
import os
import re
import shutil
import signal
import subprocess
import sys
import threading
import time
import uuid
from pathlib import Path

TEST_NAME = "TestEC4NativeScreening"
TEST_RUN = "^TestEC4NativeScreening$"
TEST_TIMEOUT = "4h"
EC4_SCREENING_TAG = "portico_ec4_screening"
TEST_COUNT = 1
PACKAGE = "./internal/store/writer"
MIN_FILES = 1
MAX_FILES = 400000
HEADROOM_BYTES = 4 << 30
BANDWIDTH_BYTES = 50_000_000
IOPS = 80


def utc_now() -> str:
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def read_text(path: str | Path) -> str | None:
    try:
        return Path(path).read_text(encoding="utf-8", errors="replace").strip()
    except OSError:
        return None


def write_json(path: Path, data: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(path.name + ".tmp")
    tmp.write_text(json.dumps(data, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    os.replace(tmp, path)


def run_capture(argv: list[str], *, cwd: str | Path | None = None,
                env: dict[str, str] | None = None, timeout: float = 120) -> subprocess.CompletedProcess:
    return subprocess.run(argv, cwd=cwd, env=env, capture_output=True, text=True,
                          timeout=timeout)


def git_source(source: Path) -> dict:
    head = run_capture(["git", "-C", str(source), "rev-parse", "HEAD"], timeout=60)
    sha = (head.stdout or "").strip()
    if head.returncode != 0 or not re.fullmatch(r"[0-9a-f]{40}", sha):
        raise RuntimeError("source checkout has no full git HEAD: " + (head.stderr or "").strip())
    dirty = run_capture(["git", "-C", str(source), "status", "--porcelain"], timeout=120)
    return {
        "actual_sha": sha,
        "dirty": bool((dirty.stdout or "").strip()),
        "dirty_entries": len([line for line in (dirty.stdout or "").splitlines() if line.strip()]),
    }


def go_mod_version(source: Path) -> str | None:
    text = read_text(source / "server" / "go.mod") or ""
    match = re.search(r"(?m)^go\s+(\S+)\s*$", text)
    return match.group(1) if match else None


def _parse_dev(text: str | None) -> tuple[int, int] | None:
    match = re.match(r"\s*(\d+):(\d+)\s*$", text or "")
    return (int(match.group(1)), int(match.group(2))) if match else None


def _dev_path_from_sysdir(sysdir: str) -> str | None:
    name = os.path.basename(os.path.realpath(sysdir))
    uevent = read_text(f"/sys/class/block/{name}/uevent") or ""
    for line in uevent.splitlines():
        if line.startswith("DEVNAME="):
            return "/dev/" + line.split("=", 1)[1]
    if os.path.exists(f"/sys/class/block/{name}/dev"):
        return "/dev/" + name
    return None


def resolve_underlying_device(major: int, minor: int) -> dict:
    """Resolve a filesystem device to its whole physical block device.

    Partitions walk to their parent disk; device-mapper/MD devices follow a
    slave first. This is enough to put a cgroup IO limit on the media disk.
    """
    notes: list[str] = []
    seen: set[tuple[int, int]] = set()
    current = (major, minor)
    for _ in range(8):
        if current in seen:
            notes.append(f"loop at {current[0]}:{current[1]}")
            break
        seen.add(current)
        sysdir = f"/sys/dev/block/{current[0]}:{current[1]}"
        if not os.path.exists(sysdir):
            notes.append(f"/sys/dev/block/{current[0]}:{current[1]} is absent")
            return {"device": None, "major": None, "minor": None, "notes": notes}
        real = os.path.realpath(sysdir)
        name = os.path.basename(real)
        slaves = os.path.join(real, "slaves")
        if os.path.isdir(slaves):
            entries = sorted(os.listdir(slaves))
            if entries:
                slave_dev = _parse_dev(read_text(os.path.join(slaves, entries[0], "dev")))
                if slave_dev is not None:
                    notes.append(f"follow {name} slave {entries[0]}")
                    current = slave_dev
                    continue
        if os.path.exists(os.path.join(real, "partition")):
            parent = os.path.dirname(real)
            parent_dev = _parse_dev(read_text(os.path.join(parent, "dev")))
            if parent_dev is not None:
                notes.append(f"partition {name} -> parent {os.path.basename(parent)}")
                current = parent_dev
                continue
        device = _dev_path_from_sysdir(real)
        resolved = _parse_dev(read_text(os.path.join(real, "dev"))) or current
        notes.append(f"whole device {name}")
        return {"device": device, "major": resolved[0], "minor": resolved[1], "notes": notes}
    return {"device": None, "major": None, "minor": None, "notes": notes}


def findmnt_source(path: Path) -> str | None:
    try:
        proc = run_capture(["findmnt", "-no", "SOURCE", "-T", str(path)], timeout=30)
    except (OSError, subprocess.TimeoutExpired):
        return None
    if proc.returncode != 0:
        return None
    source = (proc.stdout or "").splitlines()[0].strip() if (proc.stdout or "").strip() else None
    if source and "[" in source:
        source = source.split("[", 1)[0]
    return source or None


def device_identity(path: Path) -> dict:
    st = os.stat(path)
    major, minor = os.major(st.st_dev), os.minor(st.st_dev)
    resolved = resolve_underlying_device(major, minor)
    source = findmnt_source(path)
    notes = list(resolved.get("notes") or [])
    if resolved.get("device") is None and source and source.startswith("/dev/"):
        try:
            blocker = os.stat(source)
            fallback = resolve_underlying_device(os.major(blocker.st_rdev), os.minor(blocker.st_rdev))
        except OSError as exc:
            notes.append(f"findmnt fallback failed: {exc}")
        else:
            fallback.setdefault("notes", [])
            fallback["notes"] = list(fallback["notes"]) + [f"findmnt source {source}"] + notes
            resolved = fallback
            notes = resolved["notes"]
    return {
        "path": str(path),
        "mount_source": source,
        "raw_major": major,
        "raw_minor": minor,
        "raw_device": f"{major}:{minor}",
        "device": resolved.get("device"),
        "major": resolved.get("major"),
        "minor": resolved.get("minor"),
        "resolution_notes": notes,
    }


def same_device(first: dict, second: dict) -> tuple[bool, str]:
    if first.get("device") and second.get("device"):
        same = (os.path.realpath(str(first["device"])) == os.path.realpath(str(second["device"]))
                or (first.get("major"), first.get("minor")) == (second.get("major"), second.get("minor")))
        return bool(same), "underlying-device"
    same = (first.get("raw_major"), first.get("raw_minor")) == (second.get("raw_major"), second.get("raw_minor"))
    return bool(same), "filesystem-st_dev"


def ensure_task_dir(path: Path, uid: int, gid: int) -> str:
    try:
        path.mkdir(parents=True)
        return "direct"
    except FileExistsError:
        return "existing"
    except PermissionError:
        run_capture(["sudo", "-n", "mkdir", "-p", str(path)], timeout=120)
        chown = run_capture(["sudo", "-n", "chown", f"{uid}:{gid}", str(path)], timeout=120)
        if chown.returncode != 0:
            raise RuntimeError(f"cannot own task directory {path}: {(chown.stderr or '').strip()}")
        return "sudo-create"


def choose_media_root(runner_temp: Path, files: int, uid: int, gid: int) -> dict:
    need = int(files) * 4096 + HEADROOM_BYTES
    root_identity = device_identity(runner_temp)
    notes: list[str] = []
    mnt = Path("/mnt")
    if mnt.is_dir() and os.path.ismount(mnt):
        try:
            mnt_identity = device_identity(mnt)
        except OSError as exc:
            notes.append(f"/mnt identity failed: {exc}")
        else:
            same, basis = same_device(root_identity, mnt_identity)
            free = shutil.disk_usage(mnt).free
            notes.append(f"/mnt free={free} need={need} same_device={same} basis={basis}")
            if not same and free >= need:
                probe = mnt / f"ec4-screening-probe-{uuid.uuid4().hex[:8]}"
                try:
                    mode = ensure_task_dir(probe, uid, gid)
                except (OSError, RuntimeError) as exc:
                    notes.append(f"/mnt task directory unavailable: {exc}")
                else:
                    notes.append(f"/mnt writable via {mode}")
                    try:
                        probe.rmdir()
                    except OSError:
                        remove_tree(probe, notes)
                    return {
                        "base": mnt,
                        "identity": mnt_identity,
                        "root_identity": root_identity,
                        "same_device": False,
                        "same_basis": basis,
                        "selection": "mnt-different-device",
                        "notes": notes,
                    }
            elif same:
                notes.append("media stays on runner temp: /mnt is the root device")
            else:
                notes.append("media stays on runner temp: /mnt lacks required free space")
    else:
        notes.append("/mnt is not a separate mount")
    return {
        "base": runner_temp,
        "identity": root_identity,
        "root_identity": root_identity,
        "same_device": True,
        "same_basis": "runner-temp-is-root",
        "selection": "runner-temp",
        "notes": notes,
    }


def read_cgroup_entries(paths: list[Path]) -> list[dict]:
    entries: list[dict] = []
    for path in paths:
        entry = {"path": str(path)}
        for name in ("io.max", "io.stat"):
            text = read_text(path / name)
            if text is not None:
                entry[name] = text
        if len(entry) > 1:
            entries.append(entry)
    return entries


def cgroup_chain(start: Path) -> list[Path]:
    base = Path("/sys/fs/cgroup")
    paths: list[Path] = []
    current = start
    while True:
        paths.append(current)
        if current == base or current.parent == current:
            break
        current = current.parent
    paths.reverse()
    return paths


def cgroup_io_snapshot() -> list[dict]:
    rel = ""
    for line in (read_text("/proc/self/cgroup") or "").splitlines():
        if line.startswith("0::"):
            rel = line[3:]
            break
    current = Path("/sys/fs/cgroup")
    paths = [current]
    for part in [part for part in rel.split("/") if part]:
        current = current / part
        paths.append(current)
    return read_cgroup_entries(paths)


class UnitCgroupMonitor(threading.Thread):
    """Keep the last readable snapshot of the transient unit's IO controls."""

    def __init__(self, unit: str):
        super().__init__(daemon=True)
        self.unit = unit
        self.snapshot: dict | None = None
        self._stop_event = threading.Event()

    def run(self) -> None:
        candidates = [
            Path(f"/sys/fs/cgroup/system.slice/{self.unit}.service"),
            Path(f"/sys/fs/cgroup/{self.unit}.service"),
        ]
        while not self._stop_event.wait(1.0):
            for candidate in candidates:
                if not candidate.is_dir():
                    continue
                snap = {"path": str(candidate)}
                for name in ("io.max", "io.stat", "cgroup.controllers"):
                    text = read_text(candidate / name)
                    if text is not None:
                        snap[name] = text
                if len(snap) > 1:
                    snap["ancestors"] = read_cgroup_entries(cgroup_chain(candidate))
                    self.snapshot = snap
                    break

    def stop(self) -> None:
        self._stop_event.set()
        self.join(timeout=5)


def stop_unit(unit: str | None) -> dict:
    if not unit:
        return {"attempted": False, "unit": None}
    try:
        proc = run_capture(["sudo", "-n", "systemctl", "stop", unit], timeout=120)
    except (OSError, subprocess.TimeoutExpired) as exc:
        return {"attempted": True, "unit": unit, "error": str(exc)}
    return {
        "attempted": True,
        "unit": unit,
        "returncode": proc.returncode,
        "stderr": (proc.stderr or "").strip(),
    }


def kill_group(proc: subprocess.Popen) -> None:
    try:
        os.killpg(os.getpgid(proc.pid), signal.SIGTERM)
    except OSError:
        pass
    try:
        proc.wait(timeout=30)
        return
    except subprocess.TimeoutExpired:
        pass
    try:
        os.killpg(os.getpgid(proc.pid), signal.SIGKILL)
    except OSError:
        pass
    try:
        proc.wait(timeout=30)
    except subprocess.TimeoutExpired:
        pass


def run_bounded(argv: list[str], *, stdout_path: Path, stderr_path: Path,
                cwd: Path, env: dict[str, str], timeout: float,
                unit: str | None) -> dict:
    stdout_path.parent.mkdir(parents=True, exist_ok=True)
    started = time.monotonic()
    monitor = UnitCgroupMonitor(unit) if unit else None
    timed_out = False
    stop_receipt: dict | None = None
    with stdout_path.open("w", encoding="utf-8") as stdout, stderr_path.open("w", encoding="utf-8") as stderr:
        proc = subprocess.Popen(argv, cwd=str(cwd), env=env, stdout=stdout, stderr=stderr,
                                start_new_session=True, text=True)
        if monitor:
            monitor.start()
        try:
            try:
                code = proc.wait(timeout=timeout)
            except subprocess.TimeoutExpired:
                timed_out = True
                if unit:
                    stop_receipt = stop_unit(unit)
                else:
                    kill_group(proc)
                try:
                    code = proc.wait(timeout=120)
                except subprocess.TimeoutExpired:
                    kill_group(proc)
                    code = proc.wait(timeout=30)
        except BaseException:
            if unit:
                stop_unit(unit)
            else:
                kill_group(proc)
            raise
        finally:
            if monitor:
                monitor.stop()
    return {
        "exit_code": code,
        "timed_out": timed_out,
        "duration_seconds": round(time.monotonic() - started, 3),
        "unit_cgroup": monitor.snapshot if monitor else None,
        "timeout_stop": stop_receipt,
    }


def sha256_file(path: Path) -> str | None:
    try:
        digest = hashlib.sha256()
        with path.open("rb") as handle:
            for chunk in iter(lambda: handle.read(8 << 20), b""):
                digest.update(chunk)
        return digest.hexdigest()
    except OSError:
        return None


def remove_tree(path: Path, errors: list[str]) -> None:
    if not path.exists():
        return
    try:
        proc = run_capture(["rm", "-rf", "--", str(path)], timeout=900)
        if proc.returncode != 0:
            errors.append(f"cleanup {path}: {(proc.stderr or '').strip()}")
    except (OSError, subprocess.TimeoutExpired) as exc:
        errors.append(f"cleanup {path}: {exc}")


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", required=True, help="Checked-out source tree")
    parser.add_argument("--source-ref", default="", help="Requested branch or full SHA")
    parser.add_argument("--cell", required=True, choices=("ssd", "throttled-hdd"))
    parser.add_argument("--replica", required=True, type=int)
    parser.add_argument("--files", default="400000")
    parser.add_argument("--artifact", required=True, help="Evidence directory to retain")
    parser.add_argument("--timeout-seconds", type=int, default=20400,
                        help="Outer test-process bound; default leaves workflow slack")
    parser.add_argument("--keep-scratch", action="store_true")
    args = parser.parse_args(argv)
    if not re.fullmatch(r"\d+", str(args.files)):
        parser.error("--files must be an integer in 1..400000")
    args.files = int(args.files)
    if args.files < MIN_FILES or args.files > MAX_FILES:
        parser.error("--files must be in 1..400000")
    if args.replica < 1:
        parser.error("--replica must be >= 1")
    if args.timeout_seconds < 60:
        parser.error("--timeout-seconds must be >= 60")
    return args


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    source = Path(args.source).resolve()
    artifact = Path(args.artifact).resolve()
    runner_temp = Path(os.environ.get("RUNNER_TEMP") or "/tmp").resolve()
    uid, gid = os.getuid(), os.getgid()
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ") + "-" + uuid.uuid4().hex[:6]

    artifact.mkdir(parents=True, exist_ok=True)
    state: dict = {
        "schema": "ec4-screening-launcher-v1",
        "label": "SCREENING",
        "qualification": False,
        "unmerged_source": True,
        "generated_at": utc_now(),
        "cell": args.cell,
        "replica": args.replica,
        "files": args.files,
        "test_count": TEST_COUNT,
        "test": {
            "package": PACKAGE,
            "name": TEST_NAME,
            "run": TEST_RUN,
            "timeout": TEST_TIMEOUT,
            "count": TEST_COUNT,
        },
        "source": {"path": str(source), "requested_ref": args.source_ref},
        "paths": {"runner_temp": str(runner_temp), "artifact": str(artifact)},
        "devices": {},
        "io": {"limited": args.cell == "throttled-hdd"},
        "exit": {"code": None, "timed_out": False},
        "status": "running",
        "errors": [],
    }
    launcher_path = artifact / "launcher.json"
    write_json(launcher_path, state)

    work_base = runner_temp / f"ec4-screening-{args.cell}-r{args.replica}-{stamp}"
    media_base: Path | None = None
    cleanup_targets: list[Path] = [work_base]
    unit: str | None = None
    try:
        if not source.is_dir():
            raise RuntimeError(f"source checkout is not a directory: {source}")
        git = git_source(source)
        state["source"].update(git)
        state["source_sha"] = git["actual_sha"]
        state["source"]["go_mod_version"] = go_mod_version(source)
        if args.source_ref and re.fullmatch(r"[0-9a-fA-F]{40}", args.source_ref):
            if args.source_ref.lower() != git["actual_sha"]:
                raise RuntimeError(
                    f"requested full SHA {args.source_ref} but checkout is {git['actual_sha']}")

        media = choose_media_root(runner_temp, args.files, uid, gid)
        media_base = media["base"] / f"ec4-screening-{args.cell}-r{args.replica}-{stamp}-media"
        ensure_task_dir(media_base, uid, gid)
        cleanup_targets.append(media_base)
        media_parent = media_base / "media"
        media_parent.mkdir(parents=True, exist_ok=False)
        library = media_parent / f"library-{stamp}"
        if os.path.lexists(library):
            raise RuntimeError(f"library root must not exist: {library}")

        db_tmp = work_base / "tmp"
        home = work_base / "home"
        db_tmp.mkdir(parents=True, exist_ok=True)
        home.mkdir(parents=True, exist_ok=True)

        required = args.files * 4096 + HEADROOM_BYTES
        media_free = shutil.disk_usage(media_parent).free
        db_free = shutil.disk_usage(db_tmp).free
        state["paths"].update({
            "work": str(work_base),
            "db_tmp": str(db_tmp),
            "media_base": str(media_base),
            "media_parent": str(media_parent),
            "library_root": str(library),
        })
        state["devices"] = {
            "media": media["identity"],
            "root": media["root_identity"],
            "media_mount_base": str(media["base"]),
            "media_selection": media["selection"],
            "selection_notes": media["notes"],
            "free_bytes": {"media_parent": media_free, "db_tmp": db_free, "media_required": required, "db_required": 1 << 30},
            "major_minor": {
                "media_raw": media["identity"].get("raw_device"),
                "media_underlying": (f"{media['identity']['major']}:{media['identity']['minor']}"
                                     if media["identity"].get("major") is not None else None),
                "root_raw": media["root_identity"].get("raw_device"),
                "root_underlying": (f"{media['root_identity']['major']}:{media['root_identity']['minor']}"
                                    if media["root_identity"].get("major") is not None else None),
            },
            "media_device": media["identity"].get("device"),
            "root_device": media["root_identity"].get("device"),
            "db_media_same_device": bool(media["same_device"]),
            "db_media_same_device_basis": media.get("same_basis"),
        }
        db_required = 1 << 30
        if media_free < required:
            raise RuntimeError(f"media free space {media_free} is below required {required}")
        if db_free < db_required:
            raise RuntimeError(f"DB scratch free space {db_free} is below required {db_required}")

        tags_proc = run_capture([sys.executable, str(source / "scripts" / "release-build-tags.py"),
                                "--capability"], timeout=60)
        tags = (tags_proc.stdout or "").strip()
        if tags_proc.returncode != 0 or not tags:
            raise RuntimeError("capability tags unavailable: " + (tags_proc.stderr or "").strip())
        # The EC4 native screening test is deliberately tag-gated so the
        # default writer-test goldens keep their legacy storage registration.
        tag_set = [tag for tag in re.split(r"[,\s]+", tags) if tag]
        if EC4_SCREENING_TAG not in tag_set:
            tag_set.append(EC4_SCREENING_TAG)
        tags = ",".join(tag_set)
        state["build_tags"] = tags

        binary = work_base / "ec4-screen.test"
        build_cmd = ["go", "test", "-c", "-p", "2", "-tags", tags, "-o", str(binary), PACKAGE]
        build_env = dict(os.environ)
        build_env.update({"TMPDIR": str(db_tmp), "GOMAXPROCS": "2", "HOME": str(home)})
        state["commands"] = {"build": build_cmd}
        state["build"] = {"stdout": str(artifact / "build.stdout"),
                          "stderr": str(artifact / "build.stderr")}
        build_result = run_capture(build_cmd, cwd=source / "server", env=build_env, timeout=1800)
        (artifact / "build.stdout").write_text(build_result.stdout or "", encoding="utf-8")
        (artifact / "build.stderr").write_text(build_result.stderr or "", encoding="utf-8")
        if build_result.returncode != 0:
            raise RuntimeError(f"go test -c failed with exit {build_result.returncode}")
        state["binary_sha256"] = sha256_file(binary)

        test_env = {
            "PORTICO_EC4_SCREENING": "1",
            "PORTICO_EC4_FILES": str(args.files),
            "PORTICO_EC4_STORAGE": args.cell,
            "PORTICO_EC4_MEDIA_ROOT": str(library),
            "PORTICO_EC4_OUTPUT": str(artifact / "report.json"),
            "PORTICO_EC4_SOURCE": git["actual_sha"],
            "PORTICO_EC4_FEATURES": "inventory",
            "GOMAXPROCS": "2",
            "TMPDIR": str(db_tmp),
            "HOME": str(home),
            "PATH": os.environ.get("PATH", "/usr/local/bin:/usr/bin:/bin"),
        }
        binary_argv = [str(binary), f"-test.run={TEST_RUN}", "-test.v",
                       f"-test.timeout={TEST_TIMEOUT}", f"-test.count={TEST_COUNT}"]
        state["env"] = test_env
        state["io"]["ancestor_io_before"] = cgroup_io_snapshot()

        if args.cell == "throttled-hdd":
            device = media["identity"].get("device")
            if not device:
                raise RuntimeError("media block device could not be resolved for IO limits")
            check = run_capture(["sudo", "-n", "true"], timeout=30)
            if check.returncode != 0:
                raise RuntimeError("passwordless sudo is required for the systemd IO-limited cell")
            if not shutil.which("systemd-run"):
                raise RuntimeError("systemd-run is required for the throttled-hdd cell")
            unit = f"ec4-screening-{args.cell}-r{args.replica}-{uuid.uuid4().hex[:8]}"
            limits = {
                "IOReadBandwidthMax": f"{device} {BANDWIDTH_BYTES}",
                "IOReadIOPSMax": f"{device} {IOPS}",
            }
            state["io"].update({"limited": True, "limits": limits, "unit": unit,
                                "device": device})
            cmd = [
                "sudo", "-n", "systemd-run", "--wait", "--pipe", "--collect",
                f"--uid={uid}", f"--gid={gid}", f"--unit={unit}",
                "--property=IOAccounting=yes",
                f"--property=IOReadBandwidthMax={device} {BANDWIDTH_BYTES}",
                f"--property=IOReadIOPSMax={device} {IOPS}",
                "--property=PrivateTmp=no",
                f"--working-directory={source / 'server'}",
            ]
            for key in sorted(test_env):
                cmd.append(f"--setenv={key}={test_env[key]}")
            cmd.extend(binary_argv)
        else:
            cmd = binary_argv
        state["commands"]["test"] = cmd

        result = run_bounded(cmd, stdout_path=artifact / "run.stdout",
                             stderr_path=artifact / "run.stderr", cwd=source / "server",
                             env=test_env, timeout=args.timeout_seconds, unit=unit)
        state["io"]["ancestor_io_after"] = cgroup_io_snapshot()
        state["io"]["unit_cgroup"] = result.get("unit_cgroup")
        state["exit"] = {
            "code": result["exit_code"],
            "timed_out": result["timed_out"],
            "duration_seconds": result["duration_seconds"],
        }
        state["timeout_stop"] = result.get("timeout_stop")
        report = artifact / "report.json"
        ran = bool(re.search(r"=== RUN\s+" + re.escape(TEST_NAME) + r"\b",
                             read_text(artifact / "run.stdout") or ""))
        state["test_ran"] = ran
        state["report_present"] = report.exists()
        state["report_sha256"] = sha256_file(report) if report.exists() else None
        if result["timed_out"]:
            state["status"] = "timed_out"
            state["errors"].append(
                f"test exceeded {args.timeout_seconds}s"
                + (f"; stopped unit {unit}" if unit else "; stopped process group"))
        elif result["exit_code"] != 0:
            state["status"] = "failed"
            state["errors"].append(f"test binary exited {result['exit_code']}")
        elif not ran:
            state["status"] = "failed"
            state["errors"].append(f"{TEST_NAME} did not run (no matching -test.run output)")
        elif not report.exists():
            state["status"] = "failed"
            state["errors"].append(f"{TEST_NAME} passed but wrote no report.json")
        else:
            state["status"] = "passed"
        state["finished_at"] = utc_now()
        write_json(launcher_path, state)
        return 0 if state["status"] == "passed" else 1
    except KeyboardInterrupt:
        if unit:
            state["timeout_stop"] = stop_unit(unit)
        state["status"] = "interrupted"
        state["errors"].append("runner received an interrupt signal")
        state["finished_at"] = utc_now()
        write_json(launcher_path, state)
        return 130
    except Exception as exc:  # Keep evidence for every failure path.
        if unit:
            state["timeout_stop"] = stop_unit(unit)
        state["status"] = "error"
        state["errors"].append(f"{type(exc).__name__}: {exc}")
        state["finished_at"] = utc_now()
        write_json(launcher_path, state)
        return 1
    finally:
        if unit and not args.keep_scratch:
            active = run_capture(["sudo", "-n", "systemctl", "is-active", "--quiet", unit], timeout=30)
            if active.returncode == 0:
                state["cleanup_skipped"] = f"unit {unit} is still active"
                args.keep_scratch = True
        if not args.keep_scratch:
            cleanup_errors: list[str] = []
            for target in reversed(cleanup_targets):
                remove_tree(target, cleanup_errors)
            if cleanup_errors:
                state["cleanup_errors"] = cleanup_errors
                state["finished_at"] = state.get("finished_at") or utc_now()
                write_json(launcher_path, state)


def _handle_signal(signum: int, _frame) -> None:
    raise KeyboardInterrupt(f"signal {signum}")


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, _handle_signal)
    signal.signal(signal.SIGINT, _handle_signal)
    raise SystemExit(main(sys.argv[1:]))
