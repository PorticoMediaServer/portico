#!/usr/bin/env python3
"""Opt-in component memory runner. Never qualification or a production profile.

Extends QL's wave supervisor scopes: an outside-cgroup collector retains raw
memory.current/peak/stat/events, limits, monotonic timestamps and partial errors.
The child reuses the existing paired/N1 owner and memory helpers. No databases,
connection handles, SQL, issuer or grant construction enters this controller.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import signal
import select
import time
import uuid

BUDGET = 2_000_000_000
CGROOT = Path('/sys/fs/cgroup')


def snapshot(directory):
    result = {'monotonic_ns': time.monotonic_ns(), 'utc_ns': time.time_ns(), 'scope': 'whole owned component cgroup, including charged file/kernel memory', 'raw': {}, 'unavailable': {}}
    for name in ('memory.max', 'memory.swap.max', 'memory.current', 'memory.peak', 'memory.stat', 'memory.events', 'cgroup.procs'):
        try:
            result['raw'][name] = (directory / name).read_text().strip()
        except OSError as error:
            result['raw'][name] = None
            result['unavailable'][name] = str(error)
    return result


def scalar(sample, name):
    text = sample['raw'].get(name)
    return int(text) if text is not None and text.isdecimal() else None


def compare(sample):
    limit, peak = scalar(sample, 'memory.max'), scalar(sample, 'memory.peak')
    return {'requested_assigned_budget_bytes': BUDGET, 'effective_leaf_budget_bytes': limit,
            'protected_planning_reserve_bytes': 400_000_000, 'planning_operating_bytes': 1520 * 1048576,
            'planning_spendable_headroom_bytes': 1_600_000_000 - peak if peak is not None else None,
            'comparison_scope': 'raw charged peak versus assigned cap/planning reserve only; no owner-line attribution or full-profile pass',
            'kernel_charged_peak_bytes': peak, 'kernel_peak_headroom_bytes': limit - peak if limit is not None and peak is not None else None,
            'qualification': False, 'whole_server_fit': None,
            'fit_unavailable_reason': 'N1/read component only; missing scale population, SC/helpers/TMDB/media/legacy and native lifecycle coverage; raw charged peak is not owned LIVE attribution'}


def emit(stream, kind, **fields):
    stream.write(json.dumps(dict(schema='portico-ec5-component-memory-v1', kind=kind, qualification=False, **fields), separators=(',', ':')) + '\n')
    stream.flush()
    os.fsync(stream.fileno())


def join_cgroup(group, child):
    if (group / 'cgroup.procs').read_text().strip():
        (group / 'cgroup.kill').write_text('1')
    if child is not None:
        child.wait(timeout=10)
    # Kernel populated-event barrier, not a sleep/retry claim of process join.
    with (group / 'cgroup.events').open() as events:
        waiter = select.poll(); waiter.register(events, select.POLLPRI | select.POLLERR)
        deadline = time.monotonic() + 10
        while True:
            events.seek(0)
            if 'populated 0' in events.read().splitlines():
                return
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise RuntimeError('owned cgroup still populated; cleanup incomplete')
            waiter.poll(max(1, int(remaining * 1000)))


def executed_parent(text, name):
    return ('=== RUN   ' + name + '\n') in text and ('--- PASS: ' + name + ' (') in text and ('--- SKIP: ' + name + ' (') not in text


def binary_hash(binary):
    with binary.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def run(binary, output, target, config=None, sustained_subset=False):
    if os.geteuid() != 0 or not os.environ.get('SUDO_UID'):
        raise ValueError('run this opt-in controller through sudo as the standard runner user')
    uid, gid = int(os.environ['SUDO_UID']), int(os.environ['SUDO_GID'])
    if uid == 0 or not binary.is_file() or not os.access(binary, os.X_OK):
        raise ValueError('ordinary runner identity and compiled test binary required')
    def interrupted(signum, frame):
        raise InterruptedError('controller signal ' + str(signum))
    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGINT, interrupted)
    identity = 'portico-ec5-' + uuid.uuid4().hex
    output.mkdir(parents=True, exist_ok=True)
    owned = output / identity
    owned.mkdir(mode=0o700)
    os.chown(owned, uid, gid)
    group = CGROOT / identity
    created, child, killed = False, None, False
    with (owned / 'memory.ndjson').open('x') as stream:
        emit(stream, 'start', identity=identity, target=target, declared_sustained_subset=sustained_subset, binary_sha256=binary_hash(binary),
             requested_assigned_budget_bytes=BUDGET, profile='assigned M=2e9, no swap; NOT physical-2GB/M=1.6e9',
             observer_scope='controller outside measured cgroup; in-child Go/process sampler overhead included', source_archive=None, source_archive_unavailable_reason='exact Git source is retained by the external lane receipt; controller records actual binary identity',
             missing_bindings=['native WC/Effective and GV concurrent/recovery', 'scalable library constructor', 'whole server', 'complete native lifecycle/peak'])
        try:
            group.mkdir(mode=0o755); created = True
            (group / 'memory.max').write_text(str(BUDGET))
            (group / 'memory.swap.max').write_text('0')
            before = snapshot(group)
            cap = scalar(before, 'memory.max')
            if cap is None or not BUDGET - os.sysconf('SC_PAGE_SIZE') < cap <= BUDGET or scalar(before, 'memory.swap.max') != 0:
                raise ValueError('kernel cap/swap readback mismatch')
            ancestors = {}
            for directory in [group.parent, *group.parent.parents]:
                if directory != CGROOT and CGROOT not in directory.parents:
                    break
                ancestors[str(directory)] = snapshot(directory)
                inherited = scalar(ancestors[str(directory)], 'memory.max')
                if inherited is not None and inherited < cap:
                    raise ValueError('stricter ancestor memory limit; not the stated 2GB cell')
            emit(stream, 'enforced', before=before, ancestors=ancestors, physical_meminfo=Path('/proc/meminfo').read_text())
            env = {'PATH': '/usr/local/bin:/usr/bin:/bin', 'HOME': str(owned), 'TMPDIR': str(owned), 'GOMAXPROCS': '2',
                   'PORTICO_EC5_MEMORY': '1', 'PORTICO_EC5_CGROUP': str(group), 'PORTICO_LANE_EVIDENCE_DIR': str(owned)}
            args = [str(binary), '-test.v', '-test.timeout=90s', '-ec5-memory=true']
            expected_parent = {'control': 'TestEC5MemoryChildControl', 'paired': 'TestEC5PairedMemoryMeasurement', 'sustained': 'TestEC5SustainedMemoryMeasurement'}[target]
            if target == 'control':
                env['PORTICO_EC5_CHILD'] = '1'
                args += ['-test.run=^TestEC5MemoryChildControl$', '-n1-paired=true']
            else:
                # Existing strict five-field config and flags remain the ONLY
                # workload authority. Controller supplies no profile or permit.
                if not config:
                    raise ValueError('existing strict --config required for paired/sustained target')
                if target == 'paired':
                    env.update(PORTICO_WRITER_N1_PAIRED='1', PORTICO_WRITER_N1_PAIRED_CONFIG=str(config))
                    args += ['-test.run=^TestEC5PairedMemoryMeasurement$', '-n1-paired=true', '-n1-paired-route=all', '-n1-paired-cohort=native-counters']
                else:
                    env.update(PORTICO_WRITER_EC2='1', PORTICO_WRITER_EC2_CONFIG=str(config))
                    args += ['-test.run=^TestEC5SustainedMemoryMeasurement$', '-ec2-sustained=true', '-ec2-cohort=native-counters']
                    if sustained_subset:
                        args += ['-ec2-writer-pattern=inventory', '-ec2-passive-every=0', '-ec2-recovery-every=0']
            def enter():
                # Happens BEFORE exec/Go initialization. Only this freshly
                # created leaf is mutated; no global controller is enabled.
                (group / 'cgroup.procs').write_text('0')
                os.setgroups([]); os.setgid(gid); os.setuid(uid)
            with (owned / 'child.log').open('x') as log:
                child = subprocess.Popen(args, cwd=Path.cwd(), env=env, stdin=subprocess.DEVNULL, stdout=log, stderr=subprocess.STDOUT, preexec_fn=enter)
                started = time.monotonic()
                previous = time.monotonic_ns(); max_gap = 0
                while child.poll() is None:
                    now = time.monotonic_ns(); gap = now - previous; previous = now; max_gap = max(max_gap, gap)
                    emit(stream, 'sample', sample=snapshot(group), pid=child.pid, observed_gap_ns=gap)
                    if time.monotonic() - started > 120:
                        killed = True
                        (group / 'cgroup.kill').write_text('1')
                        break
                    time.sleep(0.1) # declared observer cadence, not a test timing barrier
                code = child.wait()
            after = snapshot(group)
            events = after['raw'].get('memory.events')
            oom = events is None or any(line.split()[0] in ('oom', 'oom_kill') and int(line.split()[1]) != 0 for line in events.splitlines())
            text = (owned / 'child.log').read_text()
            parent_passed = executed_parent(text, expected_parent)
            safe = parent_passed and code == 0 and not killed and not after['raw'].get('cgroup.procs') and not oom and scalar(after, 'memory.peak') is not None
            emit(stream, 'final', child_returncode=code, expected_parent=expected_parent, actual_parent_passed=parent_passed, killed=killed, physical_processes_joined=not bool(after['raw'].get('cgroup.procs')), raw=after, comparison=compare(after), component_control_complete=safe, maximum_observed_sampler_gap_ns=max_gap)
            if not safe:
                raise ValueError('component failed, OOM, missing peak, timeout or unjoined process; retain raw prefix')
        except BaseException as error:
            emit(stream, 'error', error=repr(error))
            raise
        finally:
            if created:
                join_cgroup(group, child)
                tail = snapshot(group)
                group.rmdir() # remove ONLY the unique leaf created above
                emit(stream, 'cleanup', owned_cgroup_removed=not group.exists(), final_after_process_join=tail, comparison=compare(tail))
    print(json.dumps({'output': str(owned), 'qualification': False}))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--sustained-subset', action='store_true', help='explicit N1/read/WAL subset; no FULL/checkpoint/recovery claim')
    parser.add_argument('--config', type=Path, help='existing strict paired five-field config; required for paired target')
    parser.add_argument('--target', choices=('control', 'paired', 'sustained'), required=True)
    options = parser.parse_args()
    run(options.binary.resolve(), options.output.resolve(), options.target, options.config.resolve() if options.config else None, options.sustained_subset)
