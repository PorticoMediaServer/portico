#!/usr/bin/env python3
"""Join slow EC2/EC3 operations with the 10 ms host samples and keeper PASSIVE windows.

SCREENING analysis only (never a qualification input). Reads a raw.ndjson (or
.gz) from an EC2/EC3 cell that carries `host_samples` records, and classifies each
slow foreground read and EC3/WC write by what the host was doing while it ran:

  process_pause  the sampler's own tick was late during the call (the whole
                 process was not running: GC stop-the-world, CPU starvation)
  cpu_throttle   the cgroup was throttled during the call
  cpu_starved    CPU pressure or steal covers a large part of the call
  device         IO pressure (full or some) or disk busy time covers a large
                 part of the call
  unexplained    none of the above

Counters are cumulative, so features are differences between the last sample at
or before the call start and the first sample at or after its end.
"""
import argparse
import bisect
import collections
import datetime
import gzip
import json
import sys


def parse_time(text):
    text = text.rstrip('Z')
    head, _, frac = text.partition('.')
    stamp = datetime.datetime.strptime(head, '%Y-%m-%dT%H:%M:%S').replace(tzinfo=datetime.timezone.utc)
    return int(stamp.timestamp()) * 10**9 + int((frac + '000000000')[:9])


def load(path):
    opener = gzip.open if str(path).endswith('.gz') else open
    with opener(path, 'rt') as handle:
        return [json.loads(line) for line in handle if line.strip()]


def as_object(value):
    return json.loads(value) if isinstance(value, str) else value


def extract(records):
    origin = None
    samples = []
    passives = []
    ops = []
    for r in records:
        kind = r.get('kind')
        if kind == 'host_sampler_start':
            origin = r['origin_utc_ns']
        elif kind == 'host_samples':
            if origin is None:
                raise SystemExit('host_samples before host_sampler_start')
            for s in r['samples']:
                samples.append((origin + s['at_ns'], s))
        elif kind == 'ec2_checkpoint':
            result = r['checkpoint']['result']
            if result.get('NativeStarted', '0001').startswith('0001'):
                continue
            passives.append((parse_time(result['NativeStarted']), parse_time(result['NativeEnded']), bool(result.get('Recovery'))))
        elif kind == 'foreground':
            o = as_object(r['observation'])
            if o.get('CallStarted') and o.get('CallReturned'):
                ops.append(('read ' + str(o.get('Phase')), parse_time(o['CallStarted']), parse_time(o['CallReturned'])))
        elif kind == 'ec2_wc':
            raw = r['raw']
            if raw.get('CallStarted') and raw.get('CallReturned'):
                ops.append(('write WC ' + str(raw.get('Phase')), parse_time(raw['CallStarted']), parse_time(raw['CallReturned'])))
    samples.sort(key=lambda x: x[0])
    return samples, sorted(passives), ops


DELTAS = {
    'cpu_psi_us': 'psi_cpu_some_us', 'io_some_us': 'psi_io_some_us', 'io_full_us': 'psi_io_full_us',
    'steal_jiffies': 'steal_jiffies', 'iowait_jiffies': 'iowait_jiffies', 'throttled_periods': 'cg_nr_throttled',
    'throttled_us': 'cg_throttled_usec', 'cg_io_some_us': 'cg_psi_io_some_us', 'disk_busy_ms': 'disk_io_ticks_ms',
    'disk_reads': 'disk_reads_completed', 'disk_writes': 'disk_writes_completed',
}


def features(times, samples, start, end):
    """Differences across the call plus window maxima; None when the samples miss it."""
    i = bisect.bisect_right(times, start) - 1
    j = bisect.bisect_left(times, end)
    if i < 0 or j >= len(samples):
        return None
    before, after = samples[i][1], samples[j][1]
    out = {name: after[field] - before[field] for name, field in DELTAS.items()}
    inner = [s for _, s in samples[i:j + 1]]
    out['max_tick_late_ms'] = max(s['tick_late_ns'] for s in inner) / 1e6
    out['max_in_flight'] = max(s['disk_in_flight'] for s in inner)
    out['max_dirty_pages'] = max(s['nr_dirty'] for s in inner)
    out['max_writeback_pages'] = max(s['nr_writeback'] for s in inner)
    return out


def classify(duration_ms, f):
    if f is None:
        return 'no_samples'
    cover = duration_ms * 1000.0  # µs
    if f['max_tick_late_ms'] >= max(20.0, 0.5 * duration_ms):
        return 'process_pause'
    if f['throttled_periods'] > 0 or f['throttled_us'] > 0:
        return 'cpu_throttle'
    if f['cpu_psi_us'] >= 0.5 * cover or f['steal_jiffies'] > 0:
        return 'cpu_starved'
    if f['io_full_us'] >= 0.5 * cover or f['io_some_us'] >= 0.5 * cover or f['disk_busy_ms'] * 1000.0 >= 0.5 * cover:
        return 'device'
    return 'unexplained'


def percentile(values, p):
    values = sorted(values)
    return values[min(len(values) - 1, int(len(values) * p))] if values else None


def analyse(records, slow_ms):
    samples, passives, ops = extract(records)
    times = [t for t, _ in samples]
    pstart = [p[0] for p in passives]
    groups = collections.defaultdict(lambda: {'durations': [], 'slow': collections.Counter()})
    for name, start, end in ops:
        duration_ms = (end - start) / 1e6
        k = bisect.bisect_right(pstart, end)
        overlapped = any(p[1] > start and p[0] < end for p in passives[max(0, k - 8):k])
        key = (name, 'overlapped' if overlapped else 'clear')
        groups[key]['durations'].append(duration_ms)
        if duration_ms >= slow_ms:
            groups[key]['slow'][classify(duration_ms, features(times, samples, start, end)) if samples else 'no_samples'] += 1
    table = []
    for (name, overlap), g in sorted(groups.items()):
        d = g['durations']
        table.append({'operation': name, 'passive': overlap, 'n': len(d), 'p50_ms': percentile(d, 0.5), 'p99_ms': percentile(d, 0.99),
                      'max_ms': max(d), 'slow_ms_threshold': slow_ms, 'slow': sum(g['slow'].values()), 'slow_by_cause': dict(g['slow'])})
    sampler_late = max((s['tick_late_ns'] for _, s in samples), default=None)
    return {'label': 'SCREENING analysis; not a qualification input', 'samples': len(samples), 'passive_windows': len(passives),
            'max_sampler_tick_late_ms': None if sampler_late is None else sampler_late / 1e6, 'operations': table}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument('raw')
    parser.add_argument('--slow-ms', type=float, default=25.0, help='calls at or above this duration are classified')
    args = parser.parse_args(argv)
    json.dump(analyse(load(args.raw), args.slow_ms), sys.stdout, indent=2)
    sys.stdout.write('\n')


if __name__ == '__main__':
    main()
