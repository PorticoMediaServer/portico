#!/usr/bin/env python3
"""Read-only Linux telemetry companion; no processes started or signalled."""
import argparse
import json
import os
from pathlib import Path
import shutil
import time


def integer(path):
    try:
        text = path.read_text().strip()
        return text if text == 'max' else int(text)
    except (OSError, ValueError):
        return None


def memory_available():
    for line in Path('/proc/meminfo').read_text().splitlines():
        if line.startswith('MemAvailable:'):
            return int(line.split()[1]) * 1024
    return None


def process(pid):
    # Never collect cmdline or environment: both can carry source credentials.
    try:
        stat = Path(f'/proc/{pid}/stat').read_text().rsplit(')', 1)[1].split()
        status = Path(f'/proc/{pid}/status').read_text().splitlines()
        rss = next(int(x.split()[1]) * 1024 for x in status if x.startswith('VmRSS:'))
        return {'pid': pid, 'rssBytes': rss, 'cpuTicks': int(stat[11]) + int(stat[12]),
                'threads': int(stat[17]), 'startTicks': int(stat[19])}
    except (OSError, ValueError, IndexError, StopIteration):
        return None


def cgroups(pid):
    # Record every ancestor so a tighter parent limit cannot be overlooked.
    try:
        line = next(x for x in Path(f'/proc/{pid}/cgroup').read_text().splitlines() if x.startswith('0::'))
        relative = Path(line[3:])
        if '..' in relative.parts:
            return []
        base = Path('/sys/fs/cgroup')
        current = base / str(relative).lstrip('/')
        rows = []
        while True:
            events = {}
            try:
                events = {k: int(v) for k, v in (x.split() for x in (current / 'memory.events').read_text().splitlines())}
            except (OSError, ValueError):
                pass
            rows.append({'path': str(current.relative_to(base)), 'current': integer(current / 'memory.current'),
                         'max': integer(current / 'memory.max'), 'events': events})
            if current == base:
                break
            current = current.parent
        return rows
    except (OSError, StopIteration, ValueError):
        return []


def sample(pid, database, volume):
    wal = Path(str(database) + '-wal')
    try:
        wal_bytes = wal.stat().st_size
    except FileNotFoundError:
        wal_bytes = 0
    row = {'atUnixMs': time.time_ns() // 1_000_000, 'server': process(pid),
           'memAvailableBytes': memory_available(), 'walPhysicalBytes': wal_bytes,
           'diskFreeBytes': shutil.disk_usage(database.parent).free, 'cgroups': cgroups(pid)}
    if volume:
        stat = os.statvfs(volume)
        row['volume'] = {'ceilingBytes': stat.f_blocks * stat.f_frsize,
                         'usedBytes': (stat.f_blocks - stat.f_bfree) * stat.f_frsize,
                         'freeBytes': stat.f_bavail * stat.f_frsize,
                         'freeInodes': stat.f_favail}
    return row


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--pid', type=int, required=True)
    parser.add_argument('--database', type=Path, required=True)
    parser.add_argument('--volume', type=Path, help='Optional already-mounted private tmpfs to observe; never mounts it')
    parser.add_argument('--seconds', type=float, default=1800)
    parser.add_argument('--interval', type=float, default=1)
    parser.add_argument('--out', type=Path, required=True)
    parser.add_argument('--execute', action='store_true')
    args = parser.parse_args()
    if args.pid <= 0 or not 0 < args.seconds <= 7200 or not 0.1 <= args.interval <= 60:
        parser.error('pid > 0, seconds <= 7200, interval 0.1..60 required')
    if not args.execute:
        print(json.dumps({'mode': 'plan', 'samplesMaximum': int(args.seconds / args.interval) + 1,
                          'fields': ['server CPU/RSS/threads/start', 'MemAvailable', 'physical WAL', 'disk free',
                                     'cgroup ancestor memory/current/max/events', 'optional volume statvfs'],
                          'commandLineOrEnvironmentCollected': False}))
        return
    if 'CI' in os.environ or 'GITHUB_ACTIONS' in os.environ:
        parser.error('Live batch telemetry is never run in CI')
    if not Path('/proc/meminfo').is_file() or not args.database.is_file():
        parser.error('Linux /proc and an existing database are required')
    first = process(args.pid)
    if first is None:
        parser.error('Server PID is not a readable live process')
    args.out.parent.mkdir(parents=True, exist_ok=True)
    # Exclusive creation prevents overwriting retained evidence. File paths and secrets are omitted.
    fd = os.open(args.out, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    started = time.monotonic()
    maximum_wal = 0
    with os.fdopen(fd, 'w') as output:
        index = 0
        while time.monotonic() - started < args.seconds:
            row = sample(args.pid, args.database, args.volume)
            if row['server'] is None or row['server']['startTicks'] != first['startTicks']:
                raise SystemExit('Server exited or PID was reused; telemetry incomplete')
            maximum_wal = max(maximum_wal, row['walPhysicalBytes'])
            output.write(json.dumps(row, separators=(',', ':')) + '\n')
            output.flush()
            index += 1
            time.sleep(max(0, started + index * args.interval - time.monotonic()))
    print(json.dumps({'samples': index, 'maximumWalPhysicalBytes': maximum_wal,
                      'walWithin192MiB': maximum_wal <= 192 * 1024 * 1024}))
    if maximum_wal > 192 * 1024 * 1024:
        raise SystemExit(1)


if __name__ == '__main__':
    main()
