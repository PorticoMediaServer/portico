#!/usr/bin/env python3
"""Measure a real first import and immediate unchanged rescan on an owned server.

Runs on an isolated CI host. Only the sanitized report leaves the private
temporary directory; authentication, appdata and media are removed after the
owned server stops. No catalogue rows are fabricated or edited.
"""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import secrets
import shutil
import signal
import struct
import subprocess
import tempfile
import threading
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]


def write_report(path, value):
    temporary = path.with_suffix('.json.tmp')
    temporary.write_text(json.dumps(value, indent=2) + '\n')
    temporary.replace(path)


def make_media(root, count):
    template = root / 'template.mp4'
    subprocess.run([shutil.which('ffmpeg'), '-nostdin', '-v', 'error',
                    '-f', 'lavfi', '-i', 'testsrc2=size=64x64:rate=12',
                    '-f', 'lavfi', '-i', 'sine=frequency=440:sample_rate=48000',
                    '-t', '2', '-c:v', 'libx264', '-pix_fmt', 'yuv420p',
                    '-c:a', 'aac', '-shortest', str(template)], check=True, timeout=60)
    raw = template.read_bytes()
    media = root / 'media'
    media.mkdir()
    hashes, inodes = set(), set()
    for index in range(count):
        title = f'Film {index:06d} (2000)'
        directory = media / title
        directory.mkdir()
        # A valid top-level MP4 free box gives every independently written
        # file distinct bytes without launching an encoder for every title.
        identity = f'scanner-import-{index:06d}'.encode('ascii')
        data = raw + struct.pack('>I4s', len(identity) + 8, b'free') + identity
        path = directory / (title + '.mp4')
        path.write_bytes(data)
        hashes.add(hashlib.sha256(data).digest())
        inodes.add((path.stat().st_dev, path.stat().st_ino))
    if len(hashes) != count or len(inodes) != count:
        raise AssertionError('fixture files are not distinct')
    return media, {'files': count, 'uniqueHashes': len(hashes),
                   'uniqueInodes': len(inodes), 'templateSha256': hashlib.sha256(raw).hexdigest()}


def wait_job(api, job, seconds, samples=None):
    began = time.monotonic()
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        current = api.get('/v1/admin/ingestion/jobs/' + job)
        if samples is not None:
            samples.append({'seconds': round(time.monotonic() - began, 3),
                            'phase': current.get('phase', ''), 'status': current['status'],
                            'processed': current['processed'], 'analyzed': current['analyzed']})
        # This foreground read must keep succeeding throughout ingestion.
        api.get('/v1/admin/libraries')
        if current['status'] in ('complete', 'complete_with_warnings'):
            if current['warnings']:
                raise AssertionError(f'scan completed with {current["warnings"]} warnings')
            return {key: current[key] for key in ('id', 'status', 'processed', 'analyzed', 'warnings')}
        if current['status'] in ('failed', 'cancelled', 'interrupted'):
            raise AssertionError('scan ended ' + current['status'])
        time.sleep(1)
    raise TimeoutError('the requested scan job did not finish')


def browse_ids(api, library, count, seconds):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        ids, cursor = set(), ''
        while True:
            request = {'pivot': 'movies', 'limit': 100}
            if cursor:
                request['cursor'] = cursor
            page = api.post('/v1/libraries/' + library + '/browse', request, expected=(200, 202, 409))
            if 'pageInfo' not in page:
                break  # A preparing answer or a cursor invalidated by derivation.
            info = page['pageInfo']
            if info['totalIsLowerBound'] or info['total'] != count:
                break
            rows = page['entries']
            if any(row.get('available') is not True for row in rows):
                break
            for row in rows:
                if row['id'] in ids:
                    raise AssertionError('browse returned a duplicate title')
                ids.add(row['id'])
            if len(ids) > count or time.monotonic() >= deadline:
                raise AssertionError('browse exceeded the fixture or time bound')
            if not info['hasMore']:
                if len(ids) == count:
                    return ids
                break
            next_cursor = info['nextCursor']
            if not next_cursor or next_cursor == cursor:
                raise AssertionError('browse cursor did not advance')
            cursor = next_cursor
        time.sleep(.25)
    raise TimeoutError('exact available public browse did not converge')


def helper_delta(before, after):
    a, b = before['storageHelpers'], after['storageHelpers']
    admitted, inline = b['admitted'] - a['admitted'], b['inProcess'] - a['inProcess']
    return {'operations': admitted, 'inProcess': inline, 'processStarts': admitted - inline}


class CPUProfile:
    """Sample the existing authenticated eight-second CPU endpoint.

    No runtime instrumentation or ingestion behavior changes. Windows record
    their actual bounds so the final window crossing into rescan is excluded
    from first-import attribution. Errors are types only, never HTTP bodies.
    """
    def __init__(self, api, output):
        self.api, self.output = api, output
        output.mkdir(mode=0o700)
        self.begin = time.monotonic()
        self.stop = threading.Event()
        self.windows, self.errors = [], []
        self.thread = threading.Thread(target=self.run, name='cpu-profile')
    def start(self):
        self.thread.start()
    def run(self):
        while not self.stop.is_set():
            at = time.monotonic()
            try:
                data, _ = self.api.call('GET', '/v1/admin/diagnostics/pprof/profile?seconds=8', raw=True)
                if not data.startswith(bytes([31, 139])):
                    raise ValueError('CPU profile is not gzip')
                name = f'cpu-{len(self.windows):03d}.pb.gz'
                temporary = self.output / (name + '.tmp')
                temporary.write_bytes(data)
                temporary.replace(self.output / name)
                self.windows.append({'file': name, 'beginSeconds': round(at-self.begin, 3),
                                     'endSeconds': round(time.monotonic()-self.begin, 3), 'bytes': len(data)})
            except Exception as error:
                self.errors.append(type(error).__name__)
            # 8 s sampling + 32 s pause; existing owner request/CPU bounds stay.
            self.stop.wait(32)
    def finish(self):
        self.stop.set()
        self.thread.join()
        return {'windows': self.windows, 'errors': self.errors,
                'limitations': ['Sampled Go CPU, not continuous wall time or native helper CPU.',
                                'Profiles crossing first-import completion are excluded from first-import CPU attribution.']}


def process_cpu(pid):
    # Linux reports this process and already-waited child CPU separately. This
    # is not a native-probe invocation census and is not used as wall time.
    fields = Path(f'/proc/{pid}/stat').read_text().rsplit(')', 1)[1].split()
    ticks = os.sysconf('SC_CLK_TCK')
    return {name: int(fields[index])/ticks for name, index in
            [('userSeconds', 11), ('systemSeconds', 12), ('waitedChildUserSeconds', 13), ('waitedChildSystemSeconds', 14)]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--scratch', type=Path, required=True)
    parser.add_argument('--files', type=int, default=2000)
    parser.add_argument('--timeout', type=int, default=1800)
    parser.add_argument('--observe-seconds', type=int, default=30)
    args = parser.parse_args()
    if not 1 <= args.files <= 10000 or not 1 <= args.timeout <= 3600 or not 1 <= args.observe_seconds <= 300:
        parser.error('fixture size or time bound is out of range')
    os.umask(0o077)
    def interrupted(signum, frame):
        raise KeyboardInterrupt('scanner probe interrupted')
    signal.signal(signal.SIGTERM, interrupted)
    args.scratch.mkdir(parents=True, exist_ok=True, mode=0o700)
    spec = importlib.util.spec_from_file_location('e2e_server', ROOT / 'scripts/e2e-server.py')
    e2e = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(e2e)
    report = {'head': subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip(),
              'state': 'running', 'policy': 'basic', 'scans': [],
              'runtimeBase': subprocess.check_output(['git','rev-parse','HEAD^'],cwd=ROOT,text=True).strip()}
    output = args.scratch / 'scanner-import.json'
    try:
        with tempfile.TemporaryDirectory(prefix='scanner-import-', dir=args.scratch) as directory:
            root = Path(directory)
            media, report['fixture'] = make_media(root, args.files)
            # Configure the library while empty, before its first real import.
            pending_media = root / 'pending-media'
            media.rename(pending_media)
            media.mkdir()
            exercise = e2e.Exercise(root, args.binary.resolve())
            profiler = None
            try:
                exercise.start()
                api = exercise.api
                bootstrap = api.post('/v1/setup/browser', {}, headers={'Origin': exercise.base})
                password = secrets.token_urlsafe(24)
                api.post('/v1/setup', {'setupToken': bootstrap['setupToken'], 'username': 'owner',
                         'password': password, 'name': 'Scanner fixture', 'authMode': 'local', 'interactive': True}, expected=(201,))
                signed = api.post('/v1/direct/sign-in', {'username': 'owner', 'password': password})
                api.token = signed['session']['accessToken']
                library = api.post('/v1/libraries', {'name': 'Scanner fixture', 'kind': 'movie',
                                   'path': str(media), 'metadataAgent': 'local'}, expected=(201,))['id']
                route = '/v1/admin/libraries/' + library + '/settings'
                document = api.get(route)
                saved, _ = api.call('PUT', route, {'expectedRevision': document['revision'], 'operationId': str(uuid.uuid4()),
                                    'settings': {'scanDepth': 'basic', 'analysis': ['probe', 'local_metadata', 'subtitle_discovery'],
                                                 'monitorChanges': False, 'providers': [{'mediaKind': 'movie', 'provider': 'local',
                                                                                        'apiKey': '', 'apiKeySet': False,
                                                                                        'language': '', 'region': ''}]}})
                if saved['settings']['scanDepth'] != 'basic' or saved['settings']['monitorChanges']:
                    raise AssertionError('fixture settings were not applied')
                empty = api.post('/v1/libraries/' + library + '/scans', {}, expected=(202,))
                wait_job(api, empty['id'], args.timeout)
                for path in pending_media.iterdir():
                    path.rename(media / path.name)
                previous_job = empty['id']
                profiler = CPUProfile(api, args.scratch / 'cpu-profiles')
                profiler.start()
                for phase in ('first-import', 'immediate-rescan'):
                    before = api.get('/v1/admin/diagnostics/concurrency')
                    phase_samples = []
                    cpu_before = process_cpu(exercise.process.pid)
                    started = time.monotonic()
                    queued = api.post('/v1/libraries/' + library + '/scans', {}, expected=(202,))
                    if queued['id'] == previous_job:
                        raise AssertionError('new scan reused the already completed job')
                    state = wait_job(api, queued['id'], args.timeout, phase_samples)
                    elapsed = time.monotonic() - started
                    after = api.get('/v1/admin/diagnostics/concurrency')
                    work = helper_delta(before, after)
                    if phase == 'first-import' and state['analyzed'] != args.files:
                        raise AssertionError('first import did not analyse every real file')
                    if phase == 'immediate-rescan' and (state['analyzed'] or work['processStarts']):
                        raise AssertionError('unchanged rescan probed files or started helpers')
                    cpu_after = process_cpu(exercise.process.pid)
                    report['scans'].append({'phase': phase, 'seconds': round(elapsed, 3), 'job': state, 'helpers': work,
                                            'phaseSamples': phase_samples, 'cpuDelta': {k: round(cpu_after[k]-cpu_before[k], 3) for k in cpu_before}})
                    if phase == 'first-import':
                        report['firstImportProfileEndSeconds'] = round(time.monotonic()-profiler.begin, 3)
                        profiler.stop.set()
                    write_report(output, report)
                    # Queue the rescan immediately after the first job; public
                    # convergence is checked after both jobs, not between them.
                    previous_job = queued['id']
                first_ids = browse_ids(api, library, args.files, args.timeout)
                observed = time.monotonic()
                while time.monotonic() - observed < args.observe_seconds:
                    rows = api.get('/v1/admin/libraries')['items']
                    last = next(row for row in rows if row['id'] == library)['lastScan']
                    if last['id'] != previous_job:
                        raise AssertionError('an unsolicited follow-on scan appeared')
                    time.sleep(1)
                if browse_ids(api, library, args.files, args.timeout) != first_ids:
                    raise AssertionError('public title identities changed after settling')
                log = (root / 'server.log').read_text(errors='replace')
                if 'info source input drifted' in log or 'analysis_publication_failed' in log:
                    raise AssertionError('analysis publication drift was logged')
                report.update(state='pass', availableTitles=len(first_ids), observationSeconds=args.observe_seconds,
                              foregroundLibraryReads=sum(row['method'] == 'GET' and row['path'] == '/v1/admin/libraries' for row in api.timings),
                              limitations=['One isolated hosted run, not a throughput distribution.',
                              'The follow-on observation is bounded to the recorded interval.'])
            finally:
                if profiler is not None:
                    report['cpuProfile'] = profiler.finish()
                    if not profiler.windows or profiler.errors:
                        report['profileIncomplete'] = True
                exercise.stop()
    except BaseException as error:
        # Error bodies are never copied into the artifact: they can include
        # ephemeral authentication or request data. The failure type is enough
        # for the owned CI log and the partial numeric receipt.
        report.update(state='fail', failureType=type(error).__name__)
        raise
    finally:
        write_report(output, report)
    if report.get('profileIncomplete'):
        raise RuntimeError('CPU profile did not complete')
    print(json.dumps(report, sort_keys=True))


if __name__ == '__main__':
    main()
