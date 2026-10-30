"""Separate-destination live/stopped appdata-copy recovery checks for P2-65.

This copies Portico's own state (database plus WAL/SHM, keys, config and the
disposable-scope ownership markers) to a different destination, starts a
scratch server on the copy, and verifies startup/rebase/integrity. It never
opens the live database and never writes to the source.
"""

from __future__ import annotations

import json
import os
import shutil
import sqlite3
import stat
import subprocess
import time
import urllib.error
import urllib.request
from dataclasses import dataclass, field
from pathlib import Path

from common import READINESS_PATH, Refused, check_summary, clean_absolute, clean_pass, now_iso, overall, sha256_file

DATABASE_NAME = "Database/server.sqlite"
ROOT_MARKERS = (".portico-layout-v1", ".portico-owned-v1")
SCOPE_MARKERS = ("Cache/.portico-owned-v1", "Temp/.portico-owned-v1")


@dataclass(frozen=True)
class CopyOptions:
    source_state: str
    dest_state: str
    mode: str  # live | stopped
    scope: str  # recovery | full
    binary: str
    bind: str
    out: str
    scheme: str = "http"  # http | https for the scratch readiness probe
    env: dict[str, str] = field(default_factory=dict)
    ready_timeout: float = 180.0
    stop_timeout: float = 60.0
    integrity: str = "quick"  # none | quick | full
    max_bytes: int = 50 * 1024**3
    retain: bool = False
    hash_files: bool = False
    log_path: str | None = None


def _database(state: Path) -> Path:
    return state / DATABASE_NAME


def _relative_files(source: Path, scope: str) -> tuple[list[str], list[str]]:
    if scope == "full":
        found: list[str] = []
        skipped: list[str] = []
        for root, dirs, files in os.walk(source, followlinks=False):
            root_path = Path(root)
            for name in list(dirs):
                candidate = root_path / name
                if candidate.is_symlink():
                    raise Refused(f"source state contains a symlink: {candidate}")
            for name in files:
                candidate = root_path / name
                if candidate.is_symlink():
                    raise Refused(f"source state contains a symlink: {candidate}")
                try:
                    info = candidate.lstat()
                except OSError as error:
                    raise Refused(f"could not stat source file: {error}") from error
                if not stat.S_ISREG(info.st_mode):
                    skipped.append(str(candidate.relative_to(source)))
                    continue
                found.append(str(candidate.relative_to(source)))
        return found, skipped

    if scope != "recovery":
        raise Refused("copy scope must be recovery or full")
    chosen: list[str] = []
    for name in ROOT_MARKERS:
        if (source / name).is_file():
            chosen.append(name)
    if (source / "portico.toml").is_file():
        chosen.append("portico.toml")
    for name in ("Database/server.sqlite", "Database/server.sqlite-wal", "Database/server.sqlite-shm"):
        if (source / name).is_file():
            chosen.append(name)
    keys = source / "Keys"
    if keys.is_dir():
        for root, dirs, files in os.walk(keys, followlinks=False):
            root_path = Path(root)
            for name in list(dirs):
                if (root_path / name).is_symlink():
                    raise Refused(f"source keys contain a symlink: {root_path / name}")
            for name in files:
                candidate = root_path / name
                if candidate.is_symlink():
                    raise Refused(f"source keys contain a symlink: {candidate}")
                if candidate.is_file():
                    chosen.append(str(candidate.relative_to(source)))
    for name in SCOPE_MARKERS:
        if (source / name).is_file():
            chosen.append(name)
    return sorted(set(chosen)), []


def _ordered_for_copy(relative: list[str], live: bool) -> list[str]:
    priority = {"Database/server.sqlite": 0, "Database/server.sqlite-wal": 1, "Database/server.sqlite-shm": 2}
    return sorted(relative, key=lambda name: (priority.get(name, 10), name)) if live else sorted(relative)


def _holders(database: Path) -> list[int] | None:
    if not Path("/proc").is_dir():
        return None
    target = str(database.resolve())
    found: list[int] = []
    for entry in Path("/proc").iterdir():
        if not entry.name.isdigit() or int(entry.name) == os.getpid():
            continue
        fd_dir = entry / "fd"
        try:
            for descriptor in fd_dir.iterdir():
                try:
                    if os.readlink(descriptor) == target:
                        found.append(int(entry.name))
                        break
                except OSError:
                    continue
        except OSError:
            continue
    return sorted(found)


def _copy(source: Path, dest: Path, relative: list[str], *, live: bool, max_bytes: int, hash_files: bool) -> dict:
    selected = _ordered_for_copy(relative, live)
    total = 0
    for name in selected:
        info = (source / name).lstat()
        if not stat.S_ISREG(info.st_mode):
            raise Refused(f"selected source file is not regular: {name}")
        total += info.st_size
    if total > max_bytes:
        raise Refused(f"selected copy is {total} bytes, over the {max_bytes} byte bound")
    dest.mkdir(mode=0o700, parents=False, exist_ok=True)
    records = []
    started = time.monotonic()
    started_iso = now_iso()
    for name in selected:
        src = source / name
        dst = dest / name
        dst.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        shutil.copy2(src, dst, follow_symlinks=False)
        info = dst.lstat()
        record = {
            "path": name,
            "bytes": info.st_size,
            "mtimeNs": info.st_mtime_ns,
        }
        if hash_files:
            record["sha256"] = sha256_file(dst)
        records.append(record)
    actual = sum(record["bytes"] for record in records)
    if actual > max_bytes:
        raise Refused(f"copied state is {actual} bytes, over the {max_bytes} byte bound")
    return {
        "files": records,
        "totalBytes": actual,
        "copyStartedAt": started_iso,
        "copyFinishedAt": now_iso(),
        "copyMillis": int((time.monotonic() - started) * 1000),
        "orderedDatabaseFirst": live,
    }


def inspect_copy(dest: Path, integrity: str) -> dict:
    database = _database(dest)
    out: dict = {
        "databasePresent": database.is_file(),
        "walBytes": _size(dest / "Database/server.sqlite-wal"),
        "shmBytes": _size(dest / "Database/server.sqlite-shm"),
        "integrityMode": integrity,
        "integrity": "not-run",
        "stateDirectory": None,
        "pendingRebaseJobs": None,
        "journalMode": None,
        "error": None,
    }
    if not database.is_file():
        out["error"] = "database_missing"
        return out
    try:
        connection = sqlite3.connect(database.as_uri() + "?mode=ro", uri=True, timeout=5)
        try:
            connection.execute("PRAGMA query_only=1")
            out["journalMode"] = connection.execute("PRAGMA journal_mode").fetchone()[0]
            if integrity == "quick":
                rows = [row[0] for row in connection.execute("PRAGMA quick_check").fetchall()]
                out["integrity"] = "ok" if rows == ["ok"] else "failed"
                out["integrityRows"] = rows[:20]
            elif integrity == "full":
                rows = [row[0] for row in connection.execute("PRAGMA integrity_check").fetchall()]
                out["integrity"] = "ok" if rows == ["ok"] else "failed"
                out["integrityRows"] = rows[:20]
            try:
                row = connection.execute("SELECT value FROM configuration WHERE key='state_directory'").fetchone()
                out["stateDirectory"] = row[0] if row else None
            except sqlite3.Error as error:
                out["stateDirectoryError"] = str(error)
            try:
                row = connection.execute("SELECT COUNT(*) FROM state_rebase_jobs").fetchone()
                out["pendingRebaseJobs"] = int(row[0]) if row else None
            except sqlite3.Error as error:
                out["pendingRebaseJobsError"] = str(error)
            try:
                violations = connection.execute("PRAGMA foreign_key_check").fetchmany(21)
                out["foreignKeyViolations"] = len(violations)
                out["foreignKeyViolationsTruncated"] = len(violations) == 21
            except sqlite3.Error as error:
                out["foreignKeyCheckError"] = str(error)
        finally:
            connection.close()
    except sqlite3.Error as error:
        out["error"] = str(error)
    return out


def _size(path: Path) -> int | None:
    try:
        return path.stat().st_size
    except FileNotFoundError:
        return 0
    except OSError:
        return None


class ProcessHarness:
    """A real scratch server process. The copied state is its only data folder."""

    def __init__(self, options: CopyOptions):
        self.options = options
        self.log_handle = None
        self.log_path = Path(options.log_path) if options.log_path else Path(options.out + ".server.log")

    def _environment(self, dest: Path) -> dict[str, str]:
        home = dest.parent / (dest.name + "-home")
        tmp = dest.parent / (dest.name + "-tmp")
        home.mkdir(mode=0o700, exist_ok=True)
        tmp.mkdir(mode=0o700, exist_ok=True)
        env = {
            "PATH": os.environ.get("PATH", "/usr/local/bin:/usr/bin:/bin"),
            "HOME": str(home),
            "TMPDIR": str(tmp),
            "PORTICO_DATA_DIR": str(dest),
            "PORTICO_BIND": self.options.bind,
            "PORTICO_DISCOVERY": "0",
        }
        env.update(self.options.env)
        return env

    def start(self, dest: Path):
        binary = clean_absolute(self.options.binary, must_exist=True)
        if not os.access(binary, os.X_OK):
            raise Refused("scratch server binary is not executable")
        descriptor = os.open(self.log_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        self.log_handle = os.fdopen(descriptor, "wb")
        try:
            process = subprocess.Popen(
                [str(binary)],
                env=self._environment(dest),
                cwd=str(dest),
                stdin=subprocess.DEVNULL,
                stdout=self.log_handle,
                stderr=subprocess.STDOUT,
                start_new_session=True,
            )
        except OSError as error:
            self.log_handle.close()
            self.log_handle = None
            raise Refused(f"could not start the scratch server: {error}") from error
        return process

    def ready(self, base: str, process, *, clock=time.monotonic, sleep=time.sleep) -> dict:
        deadline = clock() + self.options.ready_timeout
        attempts = 0
        last = None
        while clock() < deadline:
            attempts += 1
            if process.poll() is not None:
                return {"ready": False, "attempts": attempts, "exitCode": process.returncode, "error": "process_exited"}
            try:
                with urllib.request.urlopen(base + READINESS_PATH, timeout=3) as response:
                    if response.status == 200:
                        document = json.loads(response.read(64 * 1024))
                        if isinstance(document, dict) and document.get("ready") is True:
                            return {"ready": True, "attempts": attempts, "status": document.get("status")}
            except (urllib.error.URLError, TimeoutError, OSError, ValueError) as error:
                last = str(error)
            sleep(1.0)
        return {"ready": False, "attempts": attempts, "error": last or "timeout"}

    def stop(self, process, *, clock=time.monotonic, sleep=time.sleep) -> dict:
        if process.poll() is None:
            process.terminate()
            deadline = clock() + self.options.stop_timeout
            while clock() < deadline:
                if process.poll() is not None:
                    break
                sleep(0.5)
        forced = False
        if process.poll() is None:
            process.kill()
            forced = True
            process.wait(timeout=30)
        if self.log_handle is not None:
            self.log_handle.close()
            self.log_handle = None
        return {"clean": not forced and process.returncode == 0, "forced": forced, "exitCode": process.returncode}


def _base_url(bind: str, scheme: str) -> str:
    if scheme not in ("http", "https"):
        raise Refused("scheme must be http or https")
    host, _, port = bind.rpartition(":")
    if not host or not port.isdigit():
        raise Refused("--bind must be host:port")
    if host in ("0.0.0.0", "::", "[::]"):
        host = "127.0.0.1"
    return f"{scheme}://{host}:{port}"


def run(options: CopyOptions, *, harness=None, clock=time.monotonic, sleep=time.sleep) -> dict:
    source = clean_absolute(options.source_state, must_exist=True)
    if not source.is_dir():
        raise Refused("source state must be a directory")
    if not _database(source).is_file():
        raise Refused("source state has no Database/server.sqlite")
    dest = clean_absolute(options.dest_state)
    if dest.exists():
        if not dest.is_dir() or any(dest.iterdir()):
            raise Refused("destination must not exist or must be an empty directory")
    if dest == source or source in dest.parents or dest in source.parents:
        raise Refused("destination must be separate from the source state")
    if options.mode == "stopped":
        holders = _holders(_database(source))
        if holders:
            raise Refused(f"source database is still held by process(es) {holders}")
    elif options.mode != "live":
        raise Refused("mode must be live or stopped")

    receipt: dict = {
        "kind": "p2-65-appdata-copy-recovery",
        "version": 1,
        "mode": options.mode,
        "scope": options.scope,
        "startedAt": now_iso(),
        "source": {"state": str(source)},
        "dest": {"state": str(dest)},
        "nonAtomicCopyCaveat": (
            "A live copy of the main file and its WAL/SHM is not a transaction snapshot. "
            "This check proves whether the copy starts and recovers; it is not a backup and "
            "must not replace the product backup for a consistent live copy."
        )
        if options.mode == "live"
        else None,
    }
    dest_existed = dest.exists()
    created_dest = not dest_existed
    try:
        relative, skipped = _relative_files(source, options.scope)
        if not relative:
            raise Refused("no source files matched the copy scope")
        source_wal = _size(source / "Database/server.sqlite-wal")
        source_shm = _size(source / "Database/server.sqlite-shm")
        copied = _copy(source, dest, relative, live=options.mode == "live", max_bytes=options.max_bytes, hash_files=options.hash_files)
        receipt["copy"] = {
            "files": copied["files"],
            "totalBytes": copied["totalBytes"],
            "copyStartedAt": copied["copyStartedAt"],
            "copyFinishedAt": copied["copyFinishedAt"],
            "copyMillis": copied["copyMillis"],
            "skippedNonRegular": skipped,
            "sourceWalBytes": source_wal,
            "sourceShmBytes": source_shm,
            "destWalBytesBeforeStart": _size(dest / "Database/server.sqlite-wal"),
            "destShmBytesBeforeStart": _size(dest / "Database/server.sqlite-shm"),
        }
        harness = harness or ProcessHarness(options)
        base = _base_url(options.bind, options.scheme)
        process = harness.start(dest)
        try:
            startup = harness.ready(base, process, clock=clock, sleep=sleep)
        finally:
            stop = harness.stop(process, clock=clock, sleep=sleep)
        inspection = inspect_copy(dest, options.integrity)
        receipt["startup"] = startup
        receipt["stop"] = stop
        receipt["inspection"] = inspection
        checks = _checks(receipt, dest, options)
        receipt["thresholds"] = checks
        receipt["checkSummary"] = check_summary(checks)
        receipt["cleanPass"] = clean_pass(checks)
        receipt["claim"] = "clean-pass" if receipt["cleanPass"] else "gate-pass-with-findings-or-unavailable"
        receipt["result"] = overall(checks)
    finally:
        if created_dest and not options.retain:
            try:
                _remove_owned(dest)
                receipt.setdefault("cleanup", {})["copyRemoved"] = True
            except OSError as error:
                receipt.setdefault("cleanup", {})["copyRemoved"] = False
                receipt["cleanup"]["error"] = str(error)
        receipt.setdefault("cleanup", {})["retained"] = bool(options.retain)
        receipt["finishedAt"] = now_iso()
    return receipt


def _checks(receipt: dict, dest: Path, options: CopyOptions) -> list[dict]:
    checks: list[dict] = []

    def add(check_id: str, status: str, detail: str, value=None) -> None:
        checks.append({"id": check_id, "status": status, "detail": detail, "value": value})

    copy = receipt.get("copy") or {}
    source_wal = copy.get("sourceWalBytes")
    dest_wal = copy.get("destWalBytesBeforeStart")
    if source_wal:
        preserved = dest_wal is not None and dest_wal > 0
        add(
            "copy.walPreserved",
            "pass" if preserved else "fail",
            "a non-empty source WAL must be present in the separate copy before startup",
            {"sourceBytes": source_wal, "destBytes": dest_wal},
        )
    else:
        add("copy.walPreserved", "pass", "the source had no non-empty WAL at copy time")
    source_shm = copy.get("sourceShmBytes")
    dest_shm = copy.get("destShmBytesBeforeStart")
    if source_shm:
        preserved = dest_shm is not None and dest_shm > 0
        add(
            "copy.shmPreserved",
            "pass" if preserved else "fail",
            "a non-empty source SHM must be present in the separate copy before startup",
            {"sourceBytes": source_shm, "destBytes": dest_shm},
        )
    else:
        add("copy.shmPreserved", "pass", "the source had no non-empty SHM at copy time")

    startup = receipt.get("startup") or {}
    add(
        "startup.ready",
        "pass" if startup.get("ready") is True else "fail",
        "the scratch server must answer unauthenticated readiness on the copy",
        startup,
    )
    stop = receipt.get("stop") or {}
    add(
        "startup.cleanStop",
        "pass" if stop.get("clean") else "finding",
        "a forced or nonzero stop is a finding; the database may still recover but the stop was not clean",
        stop,
    )
    inspection = receipt.get("inspection") or {}
    state_dir = inspection.get("stateDirectory")
    add(
        "rebase.stateDirectory",
        "pass" if state_dir == str(dest) else "fail",
        "the copied database must rebase its recorded state directory to the destination",
        {"recorded": state_dir, "expected": str(dest)},
    )
    pending = inspection.get("pendingRebaseJobs")
    add(
        "rebase.noPendingJob",
        "pass" if pending == 0 else ("unavailable" if pending is None else "fail"),
        "a completed startup leaves no pending state-rebase job",
        pending,
    )
    mode = options.integrity
    if mode == "none":
        add("integrity.check", "unavailable", "integrity checking was explicitly disabled")
    elif inspection.get("integrity") == "ok":
        add("integrity.check", "pass", f"{mode} check reported ok")
    elif inspection.get("integrity") == "failed":
        add("integrity.check", "fail", f"{mode} check reported damage", inspection.get("integrityRows"))
    else:
        add(
            "integrity.check",
            "unavailable",
            "the copied database could not be inspected read-only; a WAL left by a forced stop is the likely cause",
            inspection.get("error"),
        )
    violations = inspection.get("foreignKeyViolations")
    if violations is None:
        add("integrity.foreignKeys", "unavailable", "foreign-key check did not run", inspection.get("foreignKeyCheckError"))
    else:
        add(
            "integrity.foreignKeys",
            "pass" if violations == 0 else "finding",
            "a foreign-key violation may predate the copy; it is reported for the owner, not attributed to the copy",
            {"violations": violations, "truncated": inspection.get("foreignKeyViolationsTruncated")},
        )
    return checks


def _remove_owned(path: Path) -> None:
    # The destination was created by this tool in a separate selected directory.
    shutil.rmtree(path)
    home = path.parent / (path.name + "-home")
    tmp = path.parent / (path.name + "-tmp")
    for extra in (home, tmp):
        if extra.is_dir():
            shutil.rmtree(extra, ignore_errors=True)


# ------------------------------------------------------------------ self-check
class _FakeHarness:
    def __init__(self, *, ready: bool):
        self.ready_value = ready

    def start(self, dest: Path):
        # Simulate a real startup by replacing the copied database with the
        # state a successful recovery would publish. The copy itself was real.
        for name in ("Database/server.sqlite", "Database/server.sqlite-wal", "Database/server.sqlite-shm"):
            candidate = dest / name
            if candidate.exists():
                candidate.unlink()
        connection = sqlite3.connect(dest / DATABASE_NAME)
        try:
            connection.execute("CREATE TABLE configuration(key TEXT PRIMARY KEY, value TEXT)")
            connection.execute("CREATE TABLE state_rebase_jobs(singleton INTEGER PRIMARY KEY)")
            connection.execute("INSERT INTO configuration VALUES('state_directory', ?)", (str(dest),))
            connection.commit()
        finally:
            connection.close()
        return object()

    def ready(self, base, process, *, clock=time.monotonic, sleep=time.sleep):
        return {"ready": self.ready_value, "attempts": 1}

    def stop(self, process, *, clock=time.monotonic, sleep=time.sleep):
        return {"clean": True, "forced": False, "exitCode": 0}


def _make_source(root: Path) -> Path:
    source = root / "source"
    (source / "Database").mkdir(parents=True)
    (source / "Keys").mkdir()
    (source / "Cache").mkdir()
    (source / "Temp").mkdir()
    (source / ".portico-layout-v1").write_text("a" * 64 + "\n")
    (source / "portico.toml").write_text("# fixture\n")
    (source / "Keys/key").write_text("private\n")
    (source / "Cache/.portico-owned-v1").write_text("owned\n")
    (source / "Temp/.portico-owned-v1").write_text("owned\n")
    connection = sqlite3.connect(source / DATABASE_NAME)
    try:
        connection.execute("CREATE TABLE configuration(key TEXT PRIMARY KEY, value TEXT)")
        connection.execute("CREATE TABLE state_rebase_jobs(singleton INTEGER PRIMARY KEY)")
        connection.execute("INSERT INTO configuration VALUES('state_directory', ?)", (str(source),))
        connection.commit()
    finally:
        connection.close()
    (source / "Database/server.sqlite-wal").write_bytes(b"wal-bytes")
    (source / "Database/server.sqlite-shm").write_bytes(b"shm-bytes")
    return source


def self_check() -> dict:
    import tempfile

    results = {}
    root = Path(tempfile.mkdtemp(prefix="p2-65-copy-selfcheck-"))
    try:
        source = _make_source(root)
        options = CopyOptions(
            source_state=str(source),
            dest_state=str(root / "dest"),
            mode="live",
            scope="recovery",
            binary="/bin/true",
            bind="127.0.0.1:1",
            out=str(root / "receipt.json"),
            integrity="quick",
            retain=False,
        )
        receipt = run(options, harness=_FakeHarness(ready=True))
        assert receipt["result"] == "pass", receipt
        assert receipt["copy"]["destWalBytesBeforeStart"] > 0
        assert receipt["inspection"]["stateDirectory"] == str(root / "dest")
        assert not (root / "dest").exists()
        results["copy-pass"] = "pass"

        ready_failure = CopyOptions(
            source_state=str(source),
            dest_state=str(root / "dest2"),
            mode="live",
            scope="recovery",
            binary="/bin/true",
            bind="127.0.0.1:1",
            out=str(root / "receipt2.json"),
            integrity="quick",
            retain=False,
        )
        failed = run(ready_failure, harness=_FakeHarness(ready=False))
        assert failed["result"] == "fail", failed
        results["startup-fail"] = "pass"

        nested = CopyOptions(
            source_state=str(source),
            dest_state=str(source / "nested"),
            mode="stopped",
            scope="recovery",
            binary="/bin/true",
            bind="127.0.0.1:1",
            out=str(root / "receipt3.json"),
        )
        try:
            run(nested, harness=_FakeHarness(ready=True))
        except Refused:
            results["nested-dest-refused"] = "pass"
        else:
            raise AssertionError("nested destination was not refused")
    finally:
        shutil.rmtree(root, ignore_errors=True)
    return results
