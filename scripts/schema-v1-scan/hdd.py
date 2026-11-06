#!/usr/bin/env python3
"""Run a QL-scheduled scratch scan in a new, delegated cgroup-v2 io.max leaf.

No sudo, device setup, mount, global cache drop, or parent-limit changes. The
operator supplies a delegated parent and the scratch filesystem's MAJ:MIN.
This emulates IO rate/IOPS, not rotational seek geometry or a physical HDD.
"""
import argparse
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import time
import uuid


def io_limit(device, rbps, riops):
    if not re.fullmatch(r"[0-9]+:[0-9]+", device) or rbps <= 0 or riops <= 0:
        raise ValueError("positive byte/IOPS limits and MAJ:MIN required")
    return f"{device} rbps={rbps} wbps=max riops={riops} wiops=max"


def scratch_device(root):
    # Use the filesystem's actual block-device identity, never a supplied label.
    dev = os.stat(root).st_dev
    return f"{os.major(dev)}:{os.minor(dev)}"


def run(args):
    root = Path(args.root).resolve(strict=True)
    parent = Path(args.parent).resolve(strict=True)
    if not root.is_dir() or not parent.is_relative_to(Path('/sys/fs/cgroup')):
        raise ValueError("existing scratch root and delegated /sys/fs/cgroup parent required")
    if args.device != scratch_device(root):
        raise ValueError("MAJ:MIN does not match scratch root's filesystem")
    limit = io_limit(args.device, args.rbps, args.riops)
    if 'io' not in (parent / 'cgroup.subtree_control').read_text().split():
        raise ValueError("parent has no delegated io controller; operator setup required")
    if not args.command:
        raise ValueError("scanbench command required")
    # Exclusive task-owned leaf: never move or kill anything already running.
    leaf = parent / ('portico-scan-' + uuid.uuid4().hex)
    leaf.mkdir()
    child = None
    prior_handlers = {}
    evidence = {'label': 'DIRECTIONAL', 'kind': 'cgroup-io.max-emulation',
                'device': args.device, 'requested_io_max': limit, 'cgroup': str(leaf),
                'cache': 'uncontrolled; cached metadata/media may bypass block IO',
                'physical_hdd': False}
    try:
        (leaf / 'io.max').write_text(limit + '\n')
        evidence['observed_io_max_before'] = (leaf / 'io.max').read_text()
        # preexec is used only in this single-threaded standalone launcher.
        # Parent stays outside the throttled leaf; every child/helper inherits it.
        def enter():
            os.setsid()
            (leaf / 'cgroup.procs').write_text(str(os.getpid()) + '\n')

        pending_signals = []

        def send(signum):
            try:
                os.killpg(child.pid, signum)
            except ProcessLookupError:
                pass  # child exited between poll and signal

        def forward(signum, _frame):
            if child is None:
                pending_signals.append(signum)
            elif child.poll() is None:
                send(signum)

        # Cover startup as well as the wait. A signal during Popen must not
        # leave a newly started helper behind or silently lose cancellation.
        for signum in (signal.SIGINT, signal.SIGTERM):
            prior_handlers[signum] = signal.signal(signum, forward)
        child = subprocess.Popen(args.command, preexec_fn=enter)
        for signum in pending_signals:
            send(signum)
        code = child.wait()
        evidence['exit_code'] = code
        evidence['observed_io_max_after'] = (leaf / 'io.max').read_text()
        evidence['io_stat'] = (leaf / 'io.stat').read_text()
        return code
    finally:
        try:
            # A successful parent exit is not proof its helpers exited. Kill
            # ONLY this new subtree; keep it if physical drain is unknown.
            if child is not None and child.poll() is None:
                send(signal.SIGKILL)
                child.wait()

            def populated():
                fields = (leaf / 'cgroup.events').read_text().split()
                return dict(zip(fields[::2], fields[1::2])).get('populated')

            if populated() != '0':
                (leaf / 'cgroup.kill').write_text('1\n')
            deadline = time.monotonic() + 5
            while populated() != '0':
                if time.monotonic() >= deadline:
                    raise RuntimeError(f"physical cgroup drain unproved; retained {leaf}")
                time.sleep(0.05)
            leaf.rmdir()
            evidence['cgroup_removed'] = True
        finally:
            for signum, handler in prior_handlers.items():
                signal.signal(signum, handler)
            print(json.dumps(evidence, sort_keys=True), file=sys.stderr, flush=True)



def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--parent', required=True)
    parser.add_argument('--root', required=True)
    parser.add_argument('--device', required=True)
    parser.add_argument('--rbps', type=int, default=50_000_000)
    parser.add_argument('--riops', type=int, default=80)
    parser.add_argument('command', nargs=argparse.REMAINDER)
    args = parser.parse_args()
    if args.command[:1] == ['--']:
        args.command = args.command[1:]
    try:
        return run(args)
    except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as exc:
        print(str(exc), file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
