#!/usr/bin/env python3
"""Per-file test watchdog: one process per test file, named hangs, kept results.

A whole-suite invocation hides every result behind whichever test file leaks a
worker or blocks: the suite's own timeout fires, the log shows nothing useful and
the job reports only "timed out". This module runs each selected file in its own
process group instead, with:

* the Node test runner's structured reporter appending one JSON line per test
  start/pass/fail/cancel, so a stopped run still shows every result gathered;
* counts that name their own source: when Node emitted a summary (including the
  process-wide summary a file that never loaded produces) its totals are used
  exactly as reported; with no summary at all, the counts are the test nodes that
  reported a result, with the enqueued/started/never-reported inventory beside
  them. A describe() block is never a test in that fallback;
* a failure is named once and never hidden: aggregate and file-node failures are
  kept beside the counts, and a test Node counted as cancelled but reported as a
  test:fail still names its failure and fails its file;
* a per-test limit (`--test-timeout`, passed by the caller) that turns a slow test
  into a named failure for that file only;
* a per-file watchdog that stops only the file that hangs, names the file and the
  test that was still running, and carries on with the remaining files;
* a short exit grace after the file's tests finish, so a leaked handle becomes a
  named failure instead of a stall;
* an explicit never-ran inventory when the run is interrupted.

Every exit is also an exit of the file's whole process tree. While the leader is
alive its groups and descendants are retained with their identities (PID plus
start time), and every exit path — normal, hung, leaked, interrupted or failed —
stops what the file owned before the file is classified. A child that outlives
its leader, or that cannot be stopped, is a named failure for that file, never a
pass; when one is still running after cleanup the runner starts no further file,
so a known survivor can never be mistaken for a clean result.

It is deliberately independent of the platform: a per-file log plus the structured
events are enough to report, and the caller supplies the exact argv for a file.
"""
import json
import hashlib
import os
import re
import signal
import subprocess
import sys
import tempfile
import threading
import time
from pathlib import Path

PASS = "pass"
FAIL = "fail"
HANG = "hang"
INTERRUPTED = "interrupted"

# Internal supervision verdicts: the leader exited but left something running, or
# the file's own tests finished while its process kept running.
ORPHAN = "orphan"
LEAK = "leak"

DEFAULT_TEST_TIMEOUT_MS = 60_000
DEFAULT_FILE_SECONDS = 120.0
DEFAULT_EXIT_GRACE = 5.0
DEFAULT_TERMINATE_GRACE = 5.0
POLL_SECONDS = 0.05

_TERMINAL_EVENTS = {"test:pass", "test:fail", "test:cancel"}
_FAILURE_LIMIT = 10
_MESSAGE_LIMIT = 500
# A console preview never hides a result silently: the first and last few
# failures are printed, the count in between is named, and every failure stays
# in the JSON report and the per-file event stream.
_FAILURE_PREVIEW_HEAD = 5
_FAILURE_PREVIEW_TAIL = 5
_STDERR_PREVIEW_LINES = 15
_CAUSE_DEPTH = 6
_STACK_FRAME_LIMIT = 2

_AT_FRAME = re.compile(r"^\s*at\s+(.+?)\s*$")
# V8 frames that only lead through Node or this runner; the first frame outside
# them is the test source line a reader can act on.
_NODE_FRAME = re.compile(r"(?:^|\()(?:node:|internal/|node:internal)")


def _lines(text):
    return [line.rstrip() for line in str(text or "").splitlines() if line.strip()]


def _first_line(text):
    lines = _lines(text)
    return lines[0].strip() if lines else ""


def _normalized(message):
    """The first message line, whitespace-collapsed, for grouping identical causes."""
    return re.sub(r"\s+", " ", _first_line(message)).strip()


def _first_error_line(lines):
    """The first stderr line that names an error, for a file that never loaded."""
    for line in lines:
        text = line.strip()
        if re.search(r"(?:^|\b)(?:[A-Za-z]*Error|SyntaxError|TypeError|ReferenceError|"
                     r"RangeError|AssertionError)\b", text):
            return text
    return _first_line(lines[0]) if lines else ""


def _cause_chain(error):
    """The wrapper error and its causes, bounded against a malformed or cyclic chain."""
    chain = []
    seen = set()
    while isinstance(error, dict) and len(chain) < _CAUSE_DEPTH:
        marker = id(error)
        if marker in seen:
            break
        seen.add(marker)
        chain.append(error)
        error = error.get("cause")
    return chain


def _source_frames(error):
    """First non-Node frames from the cause chain, nearest cause first."""
    chain = _cause_chain(error)
    frames = []
    # The wrapper's own stack is normally frameless, but it is still a last
    # resort when an event carried no cause at all.
    for item in [*chain[1:], *chain[:1]]:
        stack = item.get("stack")
        if not isinstance(stack, str):
            continue
        for line in stack.splitlines():
            match = _AT_FRAME.match(line)
            if not match or _NODE_FRAME.search(match.group(1)):
                continue
            frame = match.group(1).replace("file://", "").strip()
            if frame not in frames:
                frames.append(frame)
            if len(frames) >= _STACK_FRAME_LIMIT:
                return frames
    return frames[:_STACK_FRAME_LIMIT]


class FileResult:
    """One test file's outcome, including the events that justify it."""

    def __init__(self, group, file, status, *, tests=0, passed=0, failed=0,
                 cancelled=0, skipped=0, todo=0, duration=0.0, active_test=None,
                 counts_source=None, enqueued=0, started=0, unfinished=None,
                 notes=None, failures=None, wrapper_failures=None,
                 summary_counts=None, log=None, events=None, survivors=None,
                 failure_details=None, stderr=None, file_error=None):
        self.group = group
        self.file = file
        self.status = status
        self.tests = tests
        self.passed = passed
        self.failed = failed
        self.cancelled = cancelled
        self.skipped = skipped
        self.todo = todo
        self.duration = duration
        self.active_test = active_test
        # Where the counts came from, so a report never implies more evidence
        # than the file produced: "summary:file", "summary:global" (the file
        # process's only summary, e.g. a module-load failure) or "events" (no
        # terminal summary at all: the counts are the tests that finished).
        self.counts_source = counts_source
        # Event-stream inventory (test nodes, the file's own node excluded): what
        # was selected, what started, and what never reported a result.
        self.enqueued = enqueued
        self.started = started
        self.unfinished = list(unfinished or [])
        self.notes = list(notes or [])
        self.failures = list(failures or [])
        # Every failure's structured record: test name, message, declaration
        # location, cause chain and first source frames. `failures` keeps the
        # flattened display text for every one of them; the console prints only
        # a bounded preview of that list.
        self.failure_details = list(failure_details or [])
        # Aggregate nodes (a failing describe() suite, or a parent test whose only
        # error is "subtestsFailed") are reported once, by the tests they wrap.
        # They are kept here so nothing the stream contained is lost.
        self.wrapper_failures = list(wrapper_failures or [])
        # The first lines of the file's own stderr. A file that crashes before a
        # test runs has no other cause than this text; the full output stays in
        # the per-file log.
        self.stderr = list(stderr or [])
        # The file's own node failure, structured like a test failure, when Node
        # reports one (a module-load or syntax error, for example).
        self.file_error = file_error
        # The Node summary's own numbers, unchanged, whatever the counts above use.
        self.summary_counts = summary_counts
        self.log = log
        self.events = events
        # Identities this file owned that were still running after cleanup. A
        # non-empty list always means FAIL, and it stops the run.
        self.survivors = list(survivors or [])

    @property
    def ok(self):
        return self.status == PASS

    def as_dict(self):
        return {
            "group": self.group,
            "file": self.file,
            "status": self.status,
            "tests": self.tests,
            "passed": self.passed,
            "failed": self.failed,
            "cancelled": self.cancelled,
            "skipped": self.skipped,
            "todo": self.todo,
            "counts_source": self.counts_source,
            "enqueued": self.enqueued,
            "started": self.started,
            "unfinished": self.unfinished,
            "duration_seconds": round(self.duration, 3),
            "active_test": self.active_test,
            "failures": self.failures,
            "failure_details": self.failure_details,
            "stderr": self.stderr,
            "file_error": self.file_error,
            "wrapper_failures": self.wrapper_failures,
            "summary_counts": self.summary_counts,
            "notes": self.notes,
            "survivors": self.survivors,
            "log": str(self.log) if self.log else None,
            "events": str(self.events) if self.events else None,
        }


class _EventReader:
    """Incremental JSON-lines reader: complete lines only, partial writes kept."""

    def __init__(self, path):
        self.path = Path(path)
        self.offset = 0
        self.buffer = b""

    def pump(self):
        if not self.path.exists():
            return []
        with self.path.open("rb") as stream:
            stream.seek(self.offset)
            chunk = stream.read()
        self.offset += len(chunk)
        if not chunk:
            return []
        self.buffer += chunk
        *lines, self.buffer = self.buffer.split(b"\n")
        events = []
        for line in lines:
            if not line.strip():
                continue
            try:
                events.append(json.loads(line))
            except (json.JSONDecodeError, UnicodeDecodeError):
                # A line we cannot parse is not a result; leave the rest intact.
                continue
        return events


_SUBREAPER = None


def _become_subreaper():
    """Orphaned descendants are reparented here instead of init (Linux, best effort).

    Sampling /proc catches a child only while its parent is still in the tree, so a
    helper that detaches and whose parent exits in the same few milliseconds can
    escape a pure snapshot. As a child subreaper this runner is the ancestor those
    orphans are given to, which makes every one of them discoverable by parentage
    after the leader is gone. PR_SET_CHILD_SUBREAPER is Linux-only; elsewhere the
    snapshot remains the bound, and the same cleanup rules still apply.
    """
    try:
        import ctypes
        libc = ctypes.CDLL(None, use_errno=True)
        return libc.prctl(36, 1, 0, 0, 0) == 0  # PR_SET_CHILD_SUBREAPER
    except (OSError, AttributeError):
        return False


def _subreaper():
    global _SUBREAPER
    if _SUBREAPER is None:
        _SUBREAPER = _become_subreaper()
    return _SUBREAPER


def _process_table():
    """pid -> (ppid, pgrp, start time, state) read from /proc.

    The start time is what makes an identity reusable safely: a PID or group that
    now belongs to a different start time is not the process we observed, so it is
    never signalled. An empty table means /proc is not available here.
    """
    rows = {}
    proc = Path("/proc")
    if not proc.is_dir():
        return rows
    for entry in proc.iterdir():
        if not entry.name.isdigit():
            continue
        try:
            fields = (entry / "stat").read_text().rsplit(")", 1)[1].split()
            rows[int(entry.name)] = (int(fields[1]), int(fields[2]),
                                     int(fields[19]), fields[0])
        except (OSError, ValueError, IndexError):
            continue
    return rows


class _Owned:
    """The command's process tree, retained while its leader is alive.

    Node's runner and the helpers a test starts normally share the leader's new
    session, but a helper may call setsid() and leave that session. Descendants are
    therefore remembered as identities (PID plus start time) seen while the leader
    was alive, together with the process groups they belonged to, so a later exit
    can stop exactly those processes: never a global kill, and never a process that
    merely inherited a reused PID.
    """

    def __init__(self, process):
        self.leader = process.pid
        self.pids = {}    # pid -> start time at first sighting
        self.groups = {}  # pgid -> start time of the process that owns the group
        self.available = Path("/proc").is_dir()
        self.subreaper = _subreaper()
        self.capture()
        # A leader always has a /proc entry, but retain its identity explicitly so
        # a later PID reuse can never be mistaken for this file's process.
        deadline = time.monotonic() + 0.2
        while self.available and self.leader not in self.pids and time.monotonic() < deadline:
            time.sleep(POLL_SECONDS)
            self.capture()

    def capture(self):
        """Retain the leader's current descendants; call repeatedly while it lives."""
        if not self.available:
            self.pids.setdefault(self.leader, None)
            self.groups.setdefault(self.leader, None)
            self.groups.pop(os.getpgrp(), None)
            return self
        rows = _process_table()
        owned = {self.leader}
        while True:
            found = {pid for pid, row in rows.items() if row[0] in owned} - owned
            if not found:
                break
            owned.update(found)
        for pid in owned:
            row = rows.get(pid)
            if row is None:
                continue
            self.pids.setdefault(pid, row[2])
            owner = rows.get(row[1])
            self.groups.setdefault(row[1], owner[2] if owner is not None else None)
        # Our own group is never part of the tree, whatever the ancestry says.
        self.groups.pop(os.getpgrp(), None)
        return self

    def _rows(self):
        if not self.available:
            return None
        return _process_table()

    def adopt(self):
        """Take in descendants the kernel handed to this runner as their reaper.

        Only direct children of this runner that are not the file's leader can be
        here, so this never claims an unrelated process; the identity (PID plus
        start time) still has to match before anything is signalled.
        """
        if not self.available or not self.subreaper:
            return self
        rows = _process_table()
        me = os.getpid()
        for pid, row in rows.items():
            if pid == self.leader or pid in self.pids or row[0] != me:
                continue
            self.pids[pid] = row[2]
            owner = rows.get(row[1])
            self.groups.setdefault(row[1], owner[2] if owner is not None else None)
        self.groups.pop(os.getpgrp(), None)
        return self

    def reap(self):
        """Reap this runner's own finished children, never the file's leader.

        A descendant orphaned onto this process can exit before it is ever adopted
        by name, so the rule is by parentage and state: a zombie whose parent is
        this runner is ours to reap, and subprocess still owns the leader's status.
        """
        if not self.available:
            return
        rows = _process_table()
        me = os.getpid()
        for pid, row in rows.items():
            if pid == self.leader or row[0] != me or row[3] != "Z":
                continue
            try:
                os.waitpid(pid, os.WNOHANG)
            except ChildProcessError:
                pass

    def _own_identity(self, pid, rows):
        """True when `pid` is still the process this tree observed."""
        row = rows.get(pid)
        if row is None or row[3] == "Z":
            return False
        started = self.pids.get(pid)
        return started is None or row[2] == started

    def groups_alive(self):
        rows = self._rows()
        if rows is None:
            alive = set()
            for group in self.groups:
                try:
                    os.killpg(group, 0)
                except (ProcessLookupError, PermissionError):
                    continue
                alive.add(group)
            return alive
        alive = set()
        for group, started in self.groups.items():
            owner = rows.get(group)
            if owner is not None and started is not None and owner[2] != started:
                continue  # the group id now names an unrelated process
            if any(row[1] == group and row[3] != "Z" for row in rows.values()):
                alive.add(group)
        return alive

    def pids_alive(self):
        rows = self._rows()
        if rows is None:
            alive = {}
            for pid in self.pids:
                try:
                    os.kill(pid, 0)
                except (ProcessLookupError, PermissionError):
                    continue
                alive[pid] = None
            return alive
        return {pid: rows[pid] for pid in self.pids if self._own_identity(pid, rows)}

    def survivors(self):
        """Identities of this file's tree that are still running."""
        rows = self._rows()
        if rows is None:
            return sorted(set(self.pids_alive()) | self.groups_alive())
        found = set(self.pids_alive())
        for group in self.groups_alive():
            # A member that was never sampled is still this file's descendant: no
            # other process can join a group the leader created.
            found.update(pid for pid, row in rows.items()
                         if row[1] == group and row[3] != "Z")
        return sorted(found)

    def busy(self):
        return bool(self.survivors())

    def signal(self, number):
        """Send `number` to the tree only, in its groups and by verified identity."""
        rows = self._rows()
        if rows is None:
            for group in self.groups:
                try:
                    os.killpg(group, number)
                except (ProcessLookupError, PermissionError):
                    pass
            return
        groups = self.groups_alive()
        for group in groups:
            try:
                os.killpg(group, number)
            except (ProcessLookupError, PermissionError):
                pass
        for pid, row in self.pids_alive().items():
            if row[1] in groups:
                continue  # already reached through its group
            # Re-read immediately before signalling: only the exact identity is hit.
            row = _process_table().get(pid)
            if row is None or row[3] == "Z":
                continue
            started = self.pids.get(pid)
            if started is not None and row[2] != started:
                continue
            try:
                os.kill(pid, number)
            except (ProcessLookupError, PermissionError):
                pass


class Runner:
    """Run a list of files sequentially, each in its own supervised process."""

    def __init__(self, events_dir, *, file_seconds=DEFAULT_FILE_SECONDS,
                 test_timeout_ms=DEFAULT_TEST_TIMEOUT_MS, exit_grace=DEFAULT_EXIT_GRACE,
                 terminate_grace=DEFAULT_TERMINATE_GRACE, context=None):
        self.events_dir = Path(events_dir)
        self.events_dir.mkdir(parents=True, exist_ok=True)
        self.file_seconds = float(file_seconds)
        self.test_timeout_ms = int(test_timeout_ms)
        self.exit_grace = float(exit_grace)
        self.terminate_grace = float(terminate_grace)
        self.context = dict(context or {})
        self.results = []
        self.not_run = []
        self.interrupted = None
        self.requested_stop = False
        self.halted = None
        self.elapsed = 0.0
        self.summary_path = None
        self.selection_path = None
        self._entries = []
        self.live_path = None
        self._started = 0.0
        self._previous_handlers = {}

    # -- lifecycle ---------------------------------------------------------

    def run(self, entries, argv_for, stop=None):
        """Run `(group, cwd, file)` entries in order and return this runner."""
        entries = [(group, Path(cwd), str(file)) for group, cwd, file in entries]
        self._entries = list(entries)
        self._started = time.monotonic()
        self._write_selection(entries)
        self._install_handlers()
        self._print_header(entries)
        try:
            for index, entry in enumerate(entries):
                if self._stop_requested(stop):
                    self.not_run = list(entries[index:])
                    break
                try:
                    result = self._run_entry(entry, argv_for, stop)
                except Exception as error:  # a runner fault must not hide later files
                    result = FileResult(entry[0], entry[2], FAIL,
                                        notes=[f"runner error: {type(error).__name__}: {error}"])
                self.results.append(result)
                self._print_result(result)
                if result.status == INTERRUPTED:
                    self.not_run = list(entries[index + 1:])
                    break
                if result.survivors:
                    # A known owned process outlived its file's cleanup. Starting
                    # another file here would hide it behind a later result.
                    self.halted = {"file": result.file, "processes": result.survivors}
                    self.not_run = list(entries[index + 1:])
                    break
        finally:
            self._restore_handlers()
            self.elapsed = time.monotonic() - self._started
            self._finish()
        return self

    def _write_selection(self, entries):
        """Publish the selected set first, so never-ran files stay discoverable."""
        self.live_path = self.events_dir / "live.jsonl"
        self.live_path.write_text("")
        self.selection_path = self.events_dir / "selection.json"
        self.selection_path.write_text(json.dumps({
            "context": self.context,
            "settings": {
                "file_seconds": self.file_seconds,
                "test_timeout_ms": self.test_timeout_ms,
                "exit_grace": self.exit_grace,
                "terminate_grace": self.terminate_grace,
            },
            "files": [{"group": group, "cwd": str(cwd), "file": file}
                      for group, cwd, file in entries],
        }, indent=2) + "\n")

    @property
    def exit_code(self):
        if self.interrupted is not None:
            return 128 + int(self.interrupted)
        if self.requested_stop:
            return 1
        return 0 if all(result.ok for result in self.results) and not self.not_run else 1

    # -- one file ----------------------------------------------------------

    def _run_entry(self, entry, argv_for, stop):
        group, cwd, file = entry
        # "." is the working directory itself (repository root); name its report
        # files for the root rather than with a leading dot.
        label = "root" if group in ("", ".") else group.replace("/", "_")
        stem = "%s__%s" % (label, Path(file).name)
        digest = hashlib.sha256(("%s\0%s" % (group, file)).encode("utf-8")).hexdigest()[:8]
        base = "%s-%s" % (Path(stem).stem[:80], digest)
        events_path = self.events_dir / (base + ".jsonl")
        log_path = self.events_dir / (base + ".log")
        try:
            events_path.unlink()
        except FileNotFoundError:
            pass
        argv = [str(value) for value in argv_for(group, cwd, file)]
        environment = os.environ.copy()
        environment["PORTICO_TEST_EVENTS"] = str(events_path)
        if self.live_path is not None:
            environment["PORTICO_TEST_LIVE_EVENTS"] = str(self.live_path)
        started = time.monotonic()
        reader = _EventReader(events_path)
        with log_path.open("wb") as log:
            try:
                process = subprocess.Popen(
                    argv, cwd=str(cwd), stdout=log, stderr=subprocess.STDOUT,
                    start_new_session=True, env=environment)
            except OSError as error:
                return FileResult(group, file, FAIL, duration=time.monotonic() - started,
                                  notes=[f"could not start: {error}"],
                                  log=log_path, events=events_path)
            owned = _Owned(process)
            fault = None
            try:
                outcome = self._supervise(process, reader, started, stop, owned, file)
            except Exception as error:  # a supervision fault must not leak the tree
                fault = error
                outcome = None
            finally:
                # Cleanup happens on every exit, not only while the leader is alive.
                survivors = self._stop(process, owned)
            code = process.poll()
        duration = time.monotonic() - started
        if outcome is None:
            notes = [f"runner error: {type(fault).__name__}: {fault}"]
            if survivors:
                notes.append(
                    f"owned child process(es) survived cleanup ({self._names(survivors)})")
            return FileResult(group, file, FAIL, duration=duration, notes=notes,
                              log=log_path, events=events_path, survivors=survivors)
        return self._classify(group, file, outcome, code, duration, log_path, events_path,
                              survivors)

    def _settle(self, owned):
        """Allow anything the leader left behind its exit grace, then name it."""
        deadline = time.monotonic() + self.exit_grace
        while True:
            owned.adopt()
            if not owned.busy() or time.monotonic() >= deadline:
                break
            time.sleep(min(POLL_SECONDS, max(0.0, deadline - time.monotonic())))
        return owned.survivors()

    def _supervise(self, process, reader, started, stop, owned, file):
        running = {}      # (nesting, name) -> (name, since)
        counts = {"tests": 0, "passed": 0, "failed": 0, "cancelled": 0,
                  "skipped": 0, "todo": 0}
        failures = []
        failure_details = []
        wrapper_failures = []
        stderr_lines = []
        file_error = None
        event_failed = 0       # test:fail nodes, whatever Node's own categories say
        enqueued = {}          # (nesting, name) -> name, the file's own node excluded
        started_tests = {}
        finished = set()
        summary = None         # a summary that names this file
        summary_global = None  # the process-wide summary, used only when it is the only one
        summary_at = None
        last_event = started
        terminal_tests = 0
        verdict = "exit"
        stray = []

        def consume():
            nonlocal summary, summary_global, summary_at, last_event, terminal_tests
            nonlocal event_failed, file_error
            for event in reader.pump():
                data = event.get("data") or {}
                kind = event.get("type")
                if kind == "test:stderr":
                    # The file's own output. A load failure never reaches a test
                    # event, so these first lines are its only cause.
                    for line in _lines(data.get("message")):
                        if len(stderr_lines) < _STDERR_PREVIEW_LINES:
                            stderr_lines.append(line)
                    continue
                if kind == "test:summary":
                    # A file that fails to load emits one process-wide summary and
                    # no per-file summary, so keep the fileless one and use it when
                    # it is the only summary this process produced. A summary is
                    # never summed with the event stream.
                    if isinstance(data.get("counts"), dict):
                        if data.get("file"):
                            summary = data
                        else:
                            summary_global = data
                        summary_at = time.monotonic()
                    continue
                name = data.get("name")
                if name is None:
                    continue
                last_event = time.monotonic()
                nesting = data.get("nesting") or 0
                key = (nesting, name)
                if self._is_file_event(data, file):
                    # The file's own synthetic node, whatever Node spells it as.
                    # Node does not count it as a test (except when it is a file
                    # that never loaded, and then its summary says so), so it is
                    # reported through the summary and named here, never added to
                    # the counts.
                    if kind == "test:fail":
                        file_error = self._failure_detail(
                            data, file, fallback=_first_error_line(stderr_lines))
                        wrapper_failures.append("file: " + file_error["text"])
                    continue
                if kind == "test:enqueue":
                    enqueued.setdefault(key, name)
                elif kind in ("test:dequeue", "test:start"):
                    # Node reports a test as started only as its result is sent;
                    # the dequeue is the live "this test is the one running" signal.
                    running[key] = (name, time.monotonic())
                    started_tests.setdefault(key, name)
                elif kind in _TERMINAL_EVENTS:
                    running.pop(key, None)
                    finished.add(key)
                    if self._node_role(data) == "suite":
                        # A describe() block is not a test: Node counts its tests
                        # and its suites separately, so it is never a test node in
                        # the stream-only fallback either. Its failure is kept, not
                        # counted.
                        enqueued.pop(key, None)
                        started_tests.pop(key, None)
                        if kind == "test:fail":
                            wrapper_failures.append(self._failure_text(data, file))
                        continue
                    terminal_tests += 1
                    if kind == "test:pass":
                        # Node's own categories: a skipped or todo node reports as
                        # passed on the stream, and only the stream has it here.
                        if data.get("skip"):
                            counts["skipped"] += 1
                        elif data.get("todo"):
                            counts["todo"] += 1
                        else:
                            counts["passed"] += 1
                    elif kind == "test:fail":
                        counts["failed"] += 1
                        event_failed += 1
                        detail = self._failure_detail(data, file)
                        failures.append(detail["text"])
                        failure_details.append(detail)
                    else:
                        counts["cancelled"] += 1

        while True:
            consume()
            code = process.poll()
            now = time.monotonic()
            if code is None:
                # Retain this tree's identities only while its leader can still
                # create descendants; a later exit is then cleanable to the end.
                owned.capture()
                # Reap adopted zombies while the test file can still inspect its cleanup.
                # The file leader remains owned by subprocess.
                owned.reap()
            if self._stop_requested(stop):
                verdict = INTERRUPTED
                break
            if code is not None:
                # The leader exited. Anything it owned that is still running was
                # left behind by this file, whether or not its tests passed.
                stray = self._settle(owned)
                if stray:
                    verdict = ORPHAN
                break
            if summary_at is not None and now - summary_at >= self.exit_grace:
                verdict = LEAK
                break
            if now - started >= self.file_seconds:
                verdict = HANG
                break
            if summary is None and terminal_tests and not running and now - last_event >= self.exit_grace:
                verdict = LEAK
                break
            time.sleep(min(POLL_SECONDS, max(0.0, self.file_seconds - (now - started))))
        consume()  # keep the last events written just before the group was stopped

        # One authoritative source per file, never a blend: when Node emitted a
        # summary its totals are the counts, exactly as reported (a test Node
        # classifies as cancelled is cancelled, even where the stream also
        # recorded a test:fail; the failure is still named and the file still
        # fails). Only with no summary at all are the counts the test nodes the
        # stream observed.
        role = None
        if summary is not None:
            role = "file"
        elif summary_global is not None:
            role = "global"
            summary = summary_global
        if summary is not None:
            numbers = summary["counts"]
            for key in ("tests", "passed", "failed", "cancelled", "skipped", "todo"):
                if isinstance(numbers.get(key), int):
                    counts[key] = numbers[key]
        else:
            # No terminal summary at all (a hang, an interruption or a killed
            # process): report exactly the test nodes that reached a result.
            counts["tests"] = terminal_tests
        active = None
        active_age = 0.0
        if running:
            active, active_since = list(running.values())[-1]
            active_age = max(0.0, time.monotonic() - active_since)
        unfinished = [name for key, name in started_tests.items() if key not in finished]
        unfinished += [name for key, name in enqueued.items()
                       if key not in started_tests and key not in finished]
        return {
            "verdict": verdict,
            "counts": counts,
            "counts_source": "events" if summary is None else "summary:" + str(role),
            "event_failed": event_failed,
            # Every failure is kept here. The console prints a bounded preview;
            # nothing is dropped from the JSON report or the event stream.
            "failures": failures,
            "failure_details": failure_details,
            "stderr": stderr_lines,
            "file_error": file_error,
            "wrapper_failures": wrapper_failures,
            "summary": summary,
            "enqueued": len(enqueued),
            "started": len(started_tests),
            "unfinished": unfinished,
            "active_test": active,
            "active_seconds": active_age,
            "stray": stray,
        }

    @staticmethod
    def _is_file_event(data, file):
        """True for the synthetic node Node reports for the test file itself.

        Node spells that node's name three ways depending on how the file was
        given to the command (its basename, its path as passed, or the absolute
        path it resolved), so all of them name the same wrapper here.
        """
        name = data.get("name")
        if not name or data.get("nesting"):
            return False
        return name == file or Path(name).name == Path(file).name

    def _node_role(self, data):
        """'suite' for a describe() block, else 'test'.

        Node counts tests and suites separately, so a suite is never a test. The
        kind is on every event the current reporter records; an older stream that
        lacks it can only be read by its one reliable signal, the aggregate
        "subtestsFailed" error a suite always reports.
        """
        if data.get("node"):
            return "suite" if data["node"] == "suite" else "test"
        if self._error_of(data).get("failureType") == "subtestsFailed":
            return "suite"
        return "test"

    @staticmethod
    def _error_of(data):
        error = (data.get("details") or {}).get("error")
        return error if isinstance(error, dict) else (data.get("error") if isinstance(data.get("error"), dict) else {})

    def _failure_text(self, data, file=None, fallback=None):
        return self._failure_detail(data, file, fallback)["text"]

    def _failure_detail(self, data, file=None, fallback=None):
        """One failure's test, real cause, declaration location and source frame.

        The event's error is Node's `ERR_TEST_FAILURE` wrapper; its `cause` chain
        holds the class, code and stack of what actually failed. The location is
        the test's own declaration on the event (not the wrapper's frameless
        stack), and `frames` is the first source frame outside Node.
        """
        error = self._error_of(data)
        chain = _cause_chain(error)
        # Prefer the nearest cause that names a real failure: the wrapper's
        # message is often only the same text with an ERR_TEST_FAILURE code.
        primary = chain[1] if len(chain) > 1 else (chain[0] if chain else {})
        message = (primary.get("message") or (chain[0].get("message") if chain else "")
                   or data.get("message") or "")
        name = primary.get("name") or (chain[0].get("name") if chain else "") or "Error"
        code = primary.get("code") or (chain[0].get("code") if len(chain) == 1 else "")
        if fallback and _normalized(message).lower() in ("", "test failed", "failed"):
            # Node's file node only says "test failed"; the real class and code
            # are in the file's stderr, which the report prints in full below.
            message = fallback
            match = re.match(r"([A-Za-z]*Error)\b", fallback)
            if match:
                name = match.group(1)
            match = re.search(r"\[(ERR_[A-Z_]+)\]|code: ['\"](ERR_[A-Z_]+)['\"]", fallback)
            if match:
                code = match.group(1) or match.group(2)
        event_file = data.get("file")
        if file and event_file and Path(event_file).name == Path(file).name:
            # The event names the file absolutely; report it as the caller named
            # it, so a workspace-relative path stays short and portable.
            event_file = file
        line = data.get("line")
        column = data.get("column")
        location = {
            "file": event_file,
            "line": line if isinstance(line, int) else None,
            "column": column if isinstance(column, int) else None,
        }
        frames = _source_frames(error)
        place = ""
        if location["line"] is not None:
            place = f" at {location['file']}:{location['line']}"
            if location["column"] is not None:
                place += f":{location['column']}"
        frame = f" [at {frames[0]}]" if frames else ""
        test = data.get("name")
        label = f"{test}: " if test else ""
        text = f"{label}{name}{place}: {_first_line(message)[:_MESSAGE_LIMIT]}{frame}"
        return {
            "test": test,
            "name": name,
            "code": code or None,
            "message": str(message)[:4000],
            "normalized": _normalized(message),
            "location": location,
            "frames": frames,
            # The full chain (name/code/message/stack per level) stays in JSON.
            "errors": chain,
            "text": text,
        }

    @staticmethod
    def _names(pids):
        return ", ".join(f"pid {pid}" for pid in pids) or "unknown process"

    def _classify(self, group, file, outcome, code, duration, log, events, survivors):
        counts = outcome["counts"]
        verdict = outcome["verdict"]
        failures = list(outcome["failures"])
        notes = []
        if survivors:
            # Fail closed: a surviving owned process is never a pass, and the
            # runner starts no further file while it is running.
            notes.append(
                f"owned child process(es) survived cleanup ({self._names(survivors)})")
        if verdict == INTERRUPTED:
            status = INTERRUPTED
            notes.append("interrupted while this file was running")
        elif survivors:
            status = FAIL
            notes.append("no later file was started")
        elif verdict == ORPHAN:
            status = FAIL
            notes.append(
                f"the test file exited but left a detached child running "
                f"({self._names(outcome['stray'])}); a leaked descendant is never a pass")
        elif verdict == HANG:
            status = HANG
            notes.append(f"file did not finish within {self.file_seconds:g}s")
        elif counts["failed"] or (outcome["summary"] or {}).get("success") is False:
            status = FAIL
        elif verdict == LEAK:
            # Tests may all have passed, but the file never exited: that is a real
            # test bug and must never be reported as a passing file.
            status = FAIL
            notes.append(
                f"all {counts['tests']} test(s) finished but the process did not exit "
                f"within {self.exit_grace:g}s (leaked handle or timer)")
        elif code:
            status = FAIL
            notes.append(f"exited with status {code}")
        else:
            status = PASS
        if verdict == LEAK and status == FAIL and not any("leaked handle" in note for note in notes):
            notes.append("the process did not exit after its tests finished")
        if status == HANG:
            active = outcome["active_test"]
            if active:
                notes.append(f"active test: {active} (running {outcome['active_seconds']:.1f}s)")
            else:
                notes.append("active test was not reported: the file blocked before Node "
                             "flushed its events (module load, or a synchronous block)")
        if counts["cancelled"]:
            notes.append(f"{counts['cancelled']} cancelled")
        if outcome["summary"] is None:
            # Say plainly what the numbers are: the tests that finished, not the
            # tests the file selected, plus the inventory of what never reported.
            notes.append(
                f"no terminal summary: counts are the {counts['tests']} test(s) that "
                f"reported a result ({outcome['enqueued']} enqueued, "
                f"{outcome['started']} started)")
        elif outcome["counts_source"] == "summary:global":
            notes.append("no per-file summary: the process-wide summary is the only one "
                         "this file produced")
        if outcome["unfinished"]:
            notes.append("no result reported for: " + ", ".join(outcome["unfinished"]))
        file_failures = [item for item in outcome["wrapper_failures"]
                         if item.startswith("file: ")]
        suites = [item for item in outcome["wrapper_failures"]
                  if not item.startswith("file: ")]
        if file_failures:
            notes.append("the file's own node failed: "
                         + "; ".join(item[len("file: "):] for item in file_failures)
                         + " (Node reported it for the file, not for a test; the log keeps "
                           "its diagnostic)")
        if suites:
            notes.append("describe() suite failure(s), counted by their tests not by the "
                         "suite: " + "; ".join(suites))
        if outcome["event_failed"] > counts["failed"]:
            # Never hide a failure the other source saw, and never recount it:
            # Node's categories stand, the failure is named above.
            notes.append(
                f"the event stream recorded {outcome['event_failed']} failing test(s); "
                f"Node counted {counts['failed']} failed and {counts['cancelled']} "
                f"cancelled, and those are the totals kept")
        results = (counts["passed"] + counts["failed"] + counts["cancelled"]
                   + counts["skipped"] + counts["todo"])
        if outcome["summary"] is not None and results != counts["tests"]:
            notes.append(
                f"the summary's own totals do not add up: {counts['tests']} test(s) but "
                f"{results} result(s); both are kept in the report and the events")
        return FileResult(
            group, file, status, tests=counts["tests"], passed=counts["passed"],
            failed=counts["failed"], cancelled=counts["cancelled"],
            skipped=counts["skipped"], todo=counts["todo"], duration=duration,
            active_test=outcome["active_test"], counts_source=outcome["counts_source"],
            enqueued=outcome["enqueued"], started=outcome["started"],
            unfinished=outcome["unfinished"], notes=notes, failures=failures,
            wrapper_failures=outcome["wrapper_failures"],
            failure_details=outcome["failure_details"],
            stderr=outcome["stderr"], file_error=outcome["file_error"],
            summary_counts=outcome["summary"]["counts"] if outcome["summary"] else None,
            log=log, events=events, survivors=survivors)

    # -- stop --------------------------------------------------------------

    def _stop_requested(self, stop):
        if self.interrupted is not None:
            return True
        if stop is not None and stop():
            self.requested_stop = True
            return True
        return False

    def _wait(self, process, owned, seconds):
        deadline = time.monotonic() + seconds
        while True:
            process.poll()  # reap the leader as soon as it exits
            owned.adopt()
            if not owned.busy():
                try:
                    process.wait(timeout=max(0.1, seconds))
                except subprocess.TimeoutExpired:
                    return False
                return True
            if time.monotonic() >= deadline:
                return False
            time.sleep(min(POLL_SECONDS, max(0.0, deadline - time.monotonic())))

    def _stop(self, process, owned):
        """Stop everything this file owned on this exit; return what outlived it."""
        owned.adopt()
        if owned.busy():
            for number in (signal.SIGTERM, signal.SIGKILL):
                owned.signal(number)
                if self._wait(process, owned, self.terminate_grace):
                    break
        try:
            process.wait(timeout=max(0.1, self.terminate_grace))
        except subprocess.TimeoutExpired:
            # A leader that cannot be reaped is itself a survivor, and it stays named.
            pass
        owned.reap()
        return owned.survivors()

    def _install_handlers(self):
        if threading.current_thread() is not threading.main_thread():
            return
        for sig in (signal.SIGTERM, signal.SIGINT):
            self._previous_handlers[sig] = signal.getsignal(sig)
            signal.signal(sig, self._handle_signal)

    def _restore_handlers(self):
        for sig, handler in self._previous_handlers.items():
            try:
                signal.signal(sig, handler)
            except (OSError, ValueError):
                pass
        self._previous_handlers = {}

    def _handle_signal(self, signum, frame):
        self.interrupted = signum

    # -- output ------------------------------------------------------------

    def _print_header(self, entries):
        print(f"WATCHDOG {len(entries)} file(s) | per-file {self.file_seconds:g}s | "
              f"per-test {self.test_timeout_ms}ms | exit grace {self.exit_grace:g}s", flush=True)
        for key, value in self.context.items():
            print(f"  {key}: {value}", flush=True)

    def _print_result(self, result):
        if result.status == PASS:
            print(f"PASS {result.file} ({result.tests} tests, {result.duration:.1f}s)", flush=True)
            return
        label = result.status.upper()
        detail = f"{result.tests} tests, {result.failed} failed, {result.duration:.1f}s"
        print(f"{label} {result.file} ({detail})", flush=True)
        for note in result.notes:
            print(f"  {note}", flush=True)
        # A bounded preview, never a silent cap: the head and tail are printed,
        # the hidden count is named, and the full list is in the JSON report and
        # this file's event stream.
        if len(result.failures) <= _FAILURE_PREVIEW_HEAD + _FAILURE_PREVIEW_TAIL:
            head, tail, hidden = list(result.failures), [], 0
        else:
            head = result.failures[:_FAILURE_PREVIEW_HEAD]
            tail = result.failures[-_FAILURE_PREVIEW_TAIL:]
            hidden = len(result.failures) - len(head) - len(tail)
        for failure in head:
            print(f"  x {failure}", flush=True)
        if hidden:
            target = result.events or result.log
            print(f"  ... {hidden} more failure(s); full list in {target}", flush=True)
        for failure in tail:
            print(f"  x {failure}", flush=True)
        # A file-level crash (module load, syntax error) has no failing test to
        # name; its own first stderr lines are the actual cause.
        file_failed = bool(result.file_error) or any(
            item.startswith("file: ") for item in result.wrapper_failures)
        if result.stderr and (file_failed or result.counts_source == "summary:global"):
            print(f"  file stderr (first {len(result.stderr)} line(s)):", flush=True)
            for line in result.stderr:
                print(f"    {line}", flush=True)
        if result.log:
            print(f"  log: {result.log}", flush=True)

    def _finish(self):
        # Whatever was not reached stays explicit, including after a runner fault.
        if self._entries and len(self.results) + len(self.not_run) < len(self._entries):
            self.not_run = list(self.not_run) + self._entries[len(self.results) + len(self.not_run):]
        totals = {"selected": len(self.results) + len(self.not_run)}
        for status in (PASS, FAIL, HANG, INTERRUPTED):
            totals[status] = sum(1 for result in self.results if result.status == status)
        totals["not_run"] = len(self.not_run)
        if self.interrupted is not None:
            outcome = "interrupted"
        elif totals[PASS] == totals["selected"] and not self.requested_stop:
            outcome = "pass"
        else:
            outcome = "fail"
        summary = {
            "outcome": outcome,
            "elapsed_seconds": round(self.elapsed, 3),
            "settings": {
                "file_seconds": self.file_seconds,
                "test_timeout_ms": self.test_timeout_ms,
                "exit_grace": self.exit_grace,
                "terminate_grace": self.terminate_grace,
            },
            "context": self.context,
            "totals": totals,
            "halted": self.halted,
            "live_events": str(self.live_path) if self.live_path else None,
            "selection": str(self.selection_path) if self.selection_path else None,
            "results": [result.as_dict() for result in self.results],
            "not_run": [{"group": group, "file": file} for group, _, file in self.not_run],
        }
        self.summary_path = self.events_dir / "summary.json"
        self.summary_path.write_text(json.dumps(summary, indent=2) + "\n")
        print(f"RESULTS {totals[PASS]} passed, {totals[FAIL]} failed, {totals[HANG]} hung, "
              f"{totals[INTERRUPTED]} interrupted, "
              f"{totals['not_run']} not run ({totals['selected']} files, {self.elapsed:.1f}s)",
              flush=True)
        for result in self.results:
            if result.status == PASS:
                continue
            print(f"NOT PASSED {result.status.upper()} {result.file}", flush=True)
        if self.halted:
            print(f"HALT {self.halted['file']}: owned process(es) survived cleanup "
                  f"({self._names(self.halted['processes'])}); {len(self.not_run)} file(s) not run",
                  flush=True)
        for group, _, file in self.not_run:
            print(f"NOT RUN {file}", flush=True)
        print(f"EVENTS {self.events_dir}", flush=True)


def main(argv=None):
    """Run an explicit file selection through this supervisor, unchanged.

    The selection is never derived here: every file comes from `--file` or from a
    `--files-from` list (one path per line, `-` for stdin), so a caller keeps its
    exact existing selection and only replaces the whole-glob process with one
    supervised process per file. Each file is run as:

        <command...> <file>

    in `--cwd`, so the command carries the original loader, reporter and test
    flags. Anything missing or duplicated fails before a single test runs.
    """
    import argparse
    parser = argparse.ArgumentParser(
        description="Supervise an explicit list of test files, one process per file.")
    parser.add_argument("--cwd", required=True, help="directory the files and command run in")
    parser.add_argument("--report-dir", help="persistent report directory (default: a temp dir)")
    parser.add_argument("--group", default="checkpoint", help="label for these files in the report")
    parser.add_argument("--file", action="append", default=[], help="one selected file (repeatable)")
    parser.add_argument("--files-from", action="append", default=[], metavar="PATH",
                        help="read selected files from PATH, one per line (- for stdin)")
    parser.add_argument("--file-seconds", type=float, default=DEFAULT_FILE_SECONDS)
    parser.add_argument("--test-timeout-ms", type=int, default=DEFAULT_TEST_TIMEOUT_MS)
    parser.add_argument("--exit-grace", type=float, default=DEFAULT_EXIT_GRACE)
    parser.add_argument("--terminate-grace", type=float, default=DEFAULT_TERMINATE_GRACE)
    arguments = list(sys.argv[1:] if argv is None else argv)
    if "--" not in arguments:
        parser.error("-- must separate the per-file command, e.g. -- node --test")
    split = arguments.index("--")
    options = parser.parse_args(arguments[:split])
    command = arguments[split + 1:]
    if not command:
        parser.error("the per-file command after -- is empty")

    files = list(options.file)
    for source in options.files_from:
        text = sys.stdin.read() if source == "-" else Path(source).read_text()
        files.extend(line.strip() for line in text.splitlines() if line.strip())
    if not files:
        parser.error("no files selected: pass --file or --files-from")
    if len(set(files)) != len(files):
        parser.error("the selection repeats a file")
    cwd = Path(options.cwd).resolve()
    missing = [name for name in files if not (cwd / name).is_file()]
    if missing:
        parser.error("missing selected file(s) in %s: %s" % (cwd, ", ".join(missing)))

    directory = (Path(options.report_dir) if options.report_dir
                 else Path(tempfile.mkdtemp(prefix="portico-test-events-")))
    directory.mkdir(parents=True, exist_ok=True)
    runner = Runner(directory, file_seconds=options.file_seconds,
                    test_timeout_ms=options.test_timeout_ms,
                    exit_grace=options.exit_grace,
                    terminate_grace=options.terminate_grace,
                    context={"suite": options.group, "files": len(files), "cwd": str(cwd)})
    print(f"SELECTED {len(files)} file(s) in {cwd}", flush=True)
    for name in files:
        print(f"  {options.group}/{name}", flush=True)
    runner.run([(options.group, cwd, name) for name in files],
               lambda group, entry_cwd, file: [*command, file])
    return runner.exit_code


if __name__ == "__main__":
    sys.exit(main())
