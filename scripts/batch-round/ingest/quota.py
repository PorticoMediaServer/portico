#!/usr/bin/env python3
"""Private 8 GiB loop quota and O(1) 7 GiB stop observer. Plan-only by default."""
import argparse
import json
import os
import re
from pathlib import Path
import shutil
import signal
import subprocess
import time

GIB = 1024 ** 3


def run(*args):
    return subprocess.check_output(args, text=True).strip()


def private_json(path, value):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'w') as output:
        json.dump(value, output, indent=2)
        output.flush()
        os.fsync(output.fileno())


def verified(state):
    path = Path(state)
    info = path.lstat()
    if path.is_symlink() or info.st_uid != os.getuid() or info.st_mode & 0o777 != 0o600:
        raise ValueError('quota state must be owned0600')
    value = json.loads(path.read_text())
    image, mount = Path(value['image']), Path(value['mount'])
    if image.parent != path.parent or mount != path.parent / 'volume':
        raise ValueError('quota custody path mismatch')
    backing = run('losetup', '-n', '-O', 'BACK-FILE', value['loop'])
    if Path(backing).resolve() != image.resolve():
        raise ValueError('loop backing changed')
    source = run('findmnt', '-n', '-o', 'SOURCE', '--mountpoint', str(mount))
    if source != value['loop']:
        raise ValueError('mount source changed')
    return value


def require_quota(path):
    """Heavy generators refuse any ordinary host filesystem."""
    data = json.loads(run('findmnt', '-J', '-T', str(Path(path).resolve())))['filesystems'][0]
    size = os.statvfs(path)
    if not data['source'].startswith('/dev/loop') or data['fstype'] != 'ext4' or size.f_blocks * size.f_frsize > 8 * GIB:
        raise ValueError('300k execution requires the private8GiB ext4 loop quota')


def process_tree_rss(pid):
    todo, seen, rss = [pid], set(), 0
    while todo and len(seen) < 64:
        current = todo.pop()
        if current in seen:
            continue
        seen.add(current)
        proc = Path('/proc') / str(current)
        try:
            status = (proc / 'status').read_text()
            match = re.search(r'^VmRSS:\s+(\d+) kB$', status, re.M)
            if match:
                rss += int(match[1]) * 1024
            todo.extend(int(x) for x in (proc / 'task' / str(current) / 'children').read_text().split())
        except (FileNotFoundError, ProcessLookupError):
            pass
    return rss, bool(todo)


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('action', choices=['create', 'watch', 'cleanup'])
    p.add_argument('--root', required=True)
    p.add_argument('--pid', type=int)
    p.add_argument('--notify-pid', type=int, help='owned ingest/run.py supervisor to stop generators too')
    p.add_argument('--max-seconds', type=int, default=7200)
    p.add_argument('--out')
    p.add_argument('--control', help='optional private phase control for per-bucket RSS peaks')
    p.add_argument('--execute', action='store_true')
    a = p.parse_args()
    root = Path(a.root).absolute()
    base = Path.home() / 'astra-scratch' / 'P2-65'
    if not root.is_relative_to(base) or root == base or any(x.is_symlink() for x in [root, *root.parents]):
        raise ValueError('quota root must be an owned P2-65 scratch child')
    if not a.execute:
        print(json.dumps({'mode': 'plan', 'action': a.action, 'root': str(root), 'stopBytes': 7*GIB, 'kernelHardBytes': 8*GIB}))
        return
    if 'CI' in os.environ or 'GITHUB_ACTIONS' in os.environ or os.getuid() != 0 or os.environ.get('PORTICO_FIXTURE_DISPOSABLE') != 'yes':
        raise ValueError('requires explicit disposable root environment, outside CI')
    state = root / 'quota.json'
    if a.action == 'create':
        root.mkdir(mode=0o700, parents=True, exist_ok=True)
        if any(root.iterdir()) or shutil.disk_usage(root).free < 20 * GIB:
            raise ValueError('quota root must be empty with20GiB host headroom')
        image, mount = root / 'quota.ext4', root / 'volume'
        mount.mkdir(mode=0o700)
        with image.open('xb') as output:
            os.chmod(image, 0o600)
            output.truncate(8 * GIB)
        run('mkfs.ext4', '-q', '-F', '-m', '0', str(image))
        loop = run('losetup', '--find', '--show', str(image))
        try:
            run('mount', '-o', 'nosuid,nodev,noexec', loop, str(mount))
            private_json(state, {'image': str(image), 'mount': str(mount), 'loop': loop, 'hardBytes': 8*GIB})
        except BaseException:
            subprocess.run(['umount', str(mount)], capture_output=True)
            subprocess.run(['losetup', '-d', loop], check=True)
            raise
        print(json.dumps({'created': str(mount), 'hardBytes': 8*GIB}))
        return
    value = verified(state)
    mount = Path(value['mount'])
    if a.action == 'cleanup':
        run('umount', str(mount))  # Busy mounts fail; never force or touch another service.
        run('losetup', '-d', value['loop'])
        Path(value['image']).unlink()
        state.unlink()
        mount.rmdir()
        root.rmdir()
        print('quota removed')
        return
    if not a.pid or not a.out:
        raise ValueError('watch needs --pid and --out')
    proc = Path('/proc') / str(a.pid)
    command = (proc / 'cmdline').read_bytes().replace(b'\0', b' ')
    environment = (proc / 'environ').read_bytes().split(b'\0')
    if b'/astra-scratch/P2-65/' not in command or b'portico-server' not in command or not any(x.startswith(('PORTICO_DATA_DIR='+str(mount)+'/').encode()) for x in environment):
        raise ValueError('watch PID is not the owned quota fixture server')
    notify = None
    if a.notify_pid:
        notify = Path('/proc') / str(a.notify_pid)
        caller = (notify / 'cmdline').read_bytes().replace(b'\0', b' ')
        if b'ingest/run.py' not in caller or str(root.parent).encode() not in caller:
            raise ValueError('notify PID is not this owned ingest supervisor')
        notify_start = (notify / 'stat').read_text().split()[21]
    start = (proc / 'stat').read_text().split()[21]
    peak = peak_rss = 0
    phase_peaks = {}
    rss_truncated = False
    began = time.monotonic()
    outcome = 'server-stopped'
    while proc.exists():
        try:
            fields = (proc / 'stat').read_text().split()
        except FileNotFoundError:
            break
        if fields[21] != start or fields[2] == 'Z':
            break
        tree_rss, truncated = process_tree_rss(a.pid)
        peak_rss = max(peak_rss, tree_rss)
        rss_truncated = rss_truncated or truncated
        phase = 'setup'
        if a.control:
            control = Path(a.control)
            if control.exists():
                info = control.lstat()
                if control.is_symlink() or info.st_uid != os.getuid() or info.st_mode & 0o777 != 0o600 or info.st_size > 4096:
                    raise ValueError('RSS phase control must be private bounded JSON')
                try:
                    offered = json.loads(control.read_text()).get('phase')
                    if isinstance(offered, str) and re.fullmatch(r'baseline|after|phase-[1-6]', offered):
                        phase = offered
                except (ValueError, FileNotFoundError):
                    phase = 'unavailable'
        phase_peaks[phase] = max(phase_peaks.get(phase, 0), tree_rss)
        size = os.statvfs(mount)
        used = (size.f_blocks - size.f_bfree) * size.f_frsize
        peak = max(peak, used)
        if used >= 7 * GIB or time.monotonic()-began >= a.max_seconds:
            os.kill(a.pid, signal.SIGTERM)
            if notify is not None and notify.exists() and (notify / 'stat').read_text().split()[21] == notify_start:
                os.kill(a.notify_pid, signal.SIGTERM)
            outcome = '7GiB-stop' if used >= 7 * GIB else 'ingest-deadline'
            break
        time.sleep(.25)
    private_json(a.out, {'kind': 'p2-65-quota', 'outcome': outcome, 'peakAllocatedBytes': peak, 'peakTreeRSSBytes': peak_rss, 'rssPeakByPhase': phase_peaks,
                         'rssProcessBoundExceeded': rss_truncated, 'memoryBasis': '250ms sampled sum VmRSS of owned server process tree, bounded64 processes', 'stopBytes': 7*GIB, 'kernelHardBytes': 8*GIB})


if __name__ == '__main__':
    main()
