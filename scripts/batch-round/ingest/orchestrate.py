#!/usr/bin/env python3
"""Drive the real Portico scan API over phased media directories for P2-65.

Media is generated into `phase-NN/` directories under each library root, then
the server's own scan endpoint is asked to ingest that phase. The script waits
for terminal inventory, reconciles the API count against the files actually on
disk, samples resident reads and writer diagnostics, and appends the marker
JSONL that the load helper joins on.

Default is plan-only. Larger execution requires explicit 300k acknowledgement
and media plus appdata inside a private kernel quota. No invented endpoints, no metadata-only
acceptance, no rewrite of resident data.
"""
import argparse
import hashlib
import datetime
import json
import math
import os
import pathlib
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

STOP_BYTES = 7 * 1024 ** 3
HARD_BYTES = 8 * 1024 ** 3
LIB_KINDS = ('movie', 'tv', 'music')
MEDIA_KIND = {'movie': 'movies', 'tv': 'episodes', 'music': 'music'}
LEAF_PIVOTS = {'movie': 'movies', 'tv': 'episodes', 'music': 'songs'}


def refuse(message, code=2):
    print(json.dumps({'state': 'REFUSED', 'reason': message}), file=sys.stderr)
    raise SystemExit(code)


def private_file(path):
    path = pathlib.Path(path)
    if path.is_symlink() or not path.is_file() or path.stat().st_uid != os.getuid() or path.stat().st_mode & 0o777 != 0o600:
        refuse(f'expected an operator-owned 0600 regular file: {path}')
    return path


def private_dir(path, create=False):
    path = pathlib.Path(path).absolute()
    if create:
        path.mkdir(parents=True, exist_ok=True, mode=0o700)
    if path.is_symlink() or not path.is_dir() or path.stat().st_uid != os.getuid() or path.stat().st_mode & 0o077:
        refuse(f'expected a private 0700 directory: {path}')
    return path


class Refusal(Exception):
    def __init__(self, status, method, path, detail=''):
        super().__init__(f'{method} {path} -> {status}')
        self.status = status
        self.detail = detail


class Client:
    def __init__(self, base, installation):
        self.base = base.rstrip('/')
        self.installation = installation
        self.token = ''
        self.renew = None
        self.renew_at = float('inf')

    def call(self, method, path, body=None, ok=(200,), timeout=120):
        if self.renew is not None and time.time() >= self.renew_at:
            self.renew()
        data = None if body is None else json.dumps(body).encode()
        request = urllib.request.Request(self.base + path, data=data, method=method)
        request.add_header('Accept', 'application/json')
        request.add_header('X-Portico-Installation-Id', self.installation)
        if data is not None:
            request.add_header('Content-Type', 'application/json')
        if self.token:
            request.add_header('Authorization', 'Bearer ' + self.token)
        try:
            with urllib.request.urlopen(request, timeout=timeout) as response:
                payload = response.read()
                if response.status not in ok:
                    raise Refusal(response.status, method, path)
                return json.loads(payload) if payload else None
        except urllib.error.HTTPError as error:
            detail = error.read(2048).decode('utf-8', 'replace')
            raise Refusal(error.code, method, path, detail) from None

    def read(self, path, timeout=120):
        return self.call('GET', path, timeout=timeout)


def retrying(action, attempts=6):
    for attempt in range(attempts):
        try:
            return action()
        except Refusal as error:
            if error.status not in (409, 425, 429, 503, 504) or attempt == attempts - 1:
                raise
            time.sleep(min(10, 1.5 * (attempt + 1)))


def sign_in(base, username, password, installation):
    client = Client(base, installation)
    signed = retrying(lambda: client.call('POST', '/v1/direct/sign-in',
                                          {'username': username, 'password': password}))
    session = signed.get('session', signed)
    client.token = session['accessToken']
    profiles = signed.get('profiles') or []
    primary = next((entry for entry in profiles if entry.get('primary')), profiles[0] if profiles else None)
    if not primary:
        refuse('sign-in returned no profile')
    selected = retrying(lambda: client.call(
        'POST', '/v1/direct/profiles/' + urllib.parse.quote(primary['id'], safe='') + '/select',
        {'installationId': installation, 'trust': False}))
    session = selected['session']
    client.token = session['accessToken']
    client.renew_at = datetime.datetime.fromisoformat(session['expiresAt'].replace('Z', '+00:00')).timestamp()-90
    def renew():
        # This scan operator uses ordinary admission; the load cohort independently proves canonical rotation.
        fresh = sign_in(base, username, password, installation)
        client.token, client.renew_at = fresh.token, fresh.renew_at
    client.renew = renew
    return client


def libraries(client):
    document = retrying(lambda: client.read('/v1/libraries'))
    return document.get('items') or []


def resolve_libraries(client, wanted):
    found = {}
    available = libraries(client)
    for kind in LIB_KINDS:
        if wanted.get(kind):
            found[kind] = wanted[kind]
            continue
        match = [entry for entry in available if entry.get('kind') == kind]
        if len(match) != 1:
            refuse(f'cannot resolve exactly one {kind} library ({len(match)} found); pass --library-{kind}')
        found[kind] = match[0]['id']
    return found


def diagnostics(client):
    try:
        value = retrying(lambda: client.read('/v1/admin/diagnostics/concurrency'))
    except Refusal as error:
        return None, f'diagnostics refused {error.status}'
    return value, None


def scan(client, library, mode):
    path = '/v1/libraries/' + urllib.parse.quote(library, safe='') + '/scans'
    if mode:
        path += '?mode=' + urllib.parse.quote(mode, safe='')
    return retrying(lambda: client.call('POST', path, {}, ok=(200, 201, 202)))


def inventory_sources(client, library):
    document = retrying(lambda: client.read('/v1/libraries/' + urllib.parse.quote(library, safe='') + '/inventory-status'))
    return ((document or {}).get('inventory') or {}).get('sources') or []


def wait_terminal(client, library, timeout):
    deadline = time.monotonic() + timeout
    last = []
    while time.monotonic() < deadline:
        last = inventory_sources(client, library)
        statuses = {source.get('status') for source in last}
        if statuses and statuses <= {'complete', 'complete_with_warnings'}:
            return last, None
        if statuses & {'failed', 'cancelled'}:
            return last, f'inventory terminal {sorted(statuses)}'
        time.sleep(1)
    return last, 'inventory timeout'


def accepted_count(client, library, pivot):
    try:
        batch = retrying(lambda: client.call(
            'POST', '/v1/libraries/' + urllib.parse.quote(library, safe='') + '/browse/counts',
            {'pivot': pivot, 'queries': [{'id': 'all', 'query': None}]}))
        counts = batch.get('counts') or []
        if counts and not counts[0].get('totalIsLowerBound'):
            return int(counts[0].get('total') or 0), None
        return None, 'count is a lower bound'
    except Refusal as error:
        return None, f'counts refused {error.status}'


def browse_page(client, library, pivot, cursor=''):
    body = {'pivot': pivot, 'limit': 100}
    if cursor:
        body['cursor'] = cursor
    return retrying(lambda: client.call('POST', '/v1/libraries/' + urllib.parse.quote(library, safe='') + '/browse', body))


def paginated_count(client, library, pivot, cap=4000):
    total, cursor, pages = 0, '', 0
    while pages < cap:
        page = browse_page(client, library, pivot, cursor)
        total += len(page.get('entries') or [])
        cursor = (page.get('pageInfo') or {}).get('nextCursor') or ''
        pages += 1
        if not cursor:
            return total, None
    return total, 'pagination cap reached'


def choose_pivot(client, library, kind):
    # Counts must be leaf occurrences, never a parent (show/album) pivot: the
    # parent would not inflate the file count but would hide missing episodes.
    required = LEAF_PIVOTS[kind]
    try:
        capabilities = retrying(lambda: client.read('/v1/libraries/' + urllib.parse.quote(library, safe='') +
                                                     '/browse-capabilities'))
    except Refusal:
        capabilities = {}
    offered = {pivot.get('id') for pivot in capabilities.get('pivots') or []}
    if offered and required not in offered:
        refuse(f'{kind} library does not publish the leaf pivot {required!r}; refusing parent-count substitution')
    return required


def read_sample(client, libs, pivots, rounds):
    samples = {name: [] for name in ('movie_browse', 'tv_browse', 'music_browse', 'home', 'search')}
    for _ in range(rounds):
        for kind in LIB_KINDS:
            start = time.monotonic()
            browse_page(client, libs[kind], pivots[kind])
            samples[f'{kind}_browse'].append((time.monotonic() - start) * 1000)
        start = time.monotonic()
        client.read('/v1/home')
        samples['home'].append((time.monotonic() - start) * 1000)
        start = time.monotonic()
        client.read('/v1/search?q=Movie&group=movies&limit=20&record=0')
        samples['search'].append((time.monotonic() - start) * 1000)
    return samples


def operation_stats(samples):
    out = {}
    all_values = []
    for name, values in samples.items():
        out[name] = {'p50Ms': round(percentile(values, 0.50), 3),
                     'p99Ms': round(percentile(values, 0.99), 3),
                     'samples': len(values)}
        all_values.extend(values)
    out['overall'] = {'p50Ms': round(percentile(all_values, 0.50), 3),
                      'p99Ms': round(percentile(all_values, 0.99), 3),
                      'samples': len(all_values)}
    return out


def convergence_snapshot(client, libs, pivots):
    snapshot = {}
    for kind in LIB_KINDS:
        path = '/v1/libraries/' + urllib.parse.quote(libs[kind], safe='')
        counts = client.call('POST', path + '/browse/counts', {'pivot': pivots[kind], 'queries': [
            {'id': 'all', 'query': None},
            *({'id': state, 'query': {'field': 'availability', 'operator': 'equals', 'value': state}}
              for state in ('available', 'unavailable'))]})
        exact = {}
        for entry in counts.get('counts') or []:
            if entry.get('totalIsLowerBound') or not isinstance(entry.get('total'), int):
                raise ValueError('convergence requires exact maintained availability counts')
            exact[entry['id']] = entry['total']
        if set(exact) != {'all', 'available', 'unavailable'}:
            raise ValueError('convergence counts missing')
        capabilities = client.read(path + '/browse-capabilities?pivot=' + pivots[kind])
        fields = [field for field in capabilities.get('fields') or [] if field.get('facetSource')]
        facets = {}
        # Two published base facets are a bounded reconciliation sample, not every possible facet.
        for field in fields[:2]:
            source = field['facetSource']
            name = source.get('field') or field['id']
            page = client.read(path + '/facets?' + urllib.parse.urlencode({'pivot': pivots[kind], 'field': name, 'limit': 200}))
            if page.get('totalIsLowerBound') or any(value.get('countIsLowerBound') for value in page.get('values') or []):
                raise ValueError('facet reconciliation returned lower bounds')
            values = page.get('values') or []
            facets[name] = {'total': page['total'], 'sampledValues': len(values), 'limit': 200,
                            'sha256': hashlib.sha256(json.dumps(values, sort_keys=True).encode()).hexdigest()}
        if not facets:
            raise ValueError('no published facet to reconcile')
        snapshot[kind] = {'counts': exact, 'facets': facets}
    return snapshot


def reconcile(client, libs, pivots, expected, timeout=60):
    deadline = time.monotonic() + timeout
    previous = None
    last = None
    error = None
    while time.monotonic() < deadline:
        try:
            last = convergence_snapshot(client, libs, pivots)
            valid = all(last[k]['counts']['all'] == expected[k]
                        and last[k]['counts']['available'] == expected[k]
                        and last[k]['counts']['unavailable'] == 0 for k in LIB_KINDS)
            if valid and last == previous:
                return {'passed': True, 'basis': 'two stable exact leaf/availability counts and first 200 values of two offered facets per library', 'libraries': last}
            previous = last if valid else None
        except (Refusal, ValueError, KeyError) as failure:
            error = str(failure)
        time.sleep(1)
    return {'passed': False, 'libraries': last, 'error': error or 'counts/facets did not converge'}


def write_control(path, phase, items, event):
    if not path:
        return
    # load.ts reads a JSON control object (baseline/during/after) atomically; the
    # JSONL marker is the separate join/receipt stream. Keep this file event-free
    # so it takes the JSON branch instead of the JSONL branch.
    control = {'phase': 'after' if event == 'fixture-end' else ('baseline' if event == 'baseline' else f'phase-{phase}'), 'ingestedItems': items,
               'at': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()),
               'event': event}
    temporary = pathlib.Path(str(path) + '.tmp')
    fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, 'w') as handle:
        json.dump(control, handle, separators=(',', ':'))
        handle.write('\n')
        handle.flush()
        os.fsync(handle.fileno())
    os.replace(temporary, path)


def percentile(values, fraction):
    if not values:
        return None
    ordered = sorted(values)
    return ordered[max(0, min(len(ordered) - 1, math.ceil(fraction * len(ordered)) - 1))]


def append_marker(path, record):
    fd = os.open(path, os.O_WRONLY | os.O_APPEND | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    os.fchmod(fd, 0o600)
    with os.fdopen(fd, 'a') as handle:
        handle.write(json.dumps(record, separators=(',', ':')) + '\n')
        handle.flush()
        os.fsync(handle.fileno())


def tree_bytes(root):
    total = 0
    for current, _, files in os.walk(root, followlinks=False):
        try:
            total += os.lstat(current).st_blocks * 512
        except OSError:
            pass
        for name in files:
            try:
                info = os.lstat(os.path.join(current, name))
            except OSError:
                continue
            if not os.path.islink(os.path.join(current, name)):
                total += info.st_blocks * 512
    return total


def generate_phase(media, scratch, total, phase, phase_size, facts, generation):
    command = ['python3', str(media), '--root', str(scratch), '--total', str(total),
               '--phase', str(phase), '--phase-size', str(phase_size),
               '--generation', generation,
               '--facts', str(facts), '--execute']
    if total > 100:
        command.append('--acknowledge-300k')
    attempt = subprocess.run(command, capture_output=True, text=True)
    if attempt.returncode != 0:
        refuse(f'media generation failed for phase {phase}: {attempt.stderr.strip()[:400]}', 3)
    return json.loads(attempt.stdout.strip().splitlines()[-1])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base', default='http://127.0.0.1:32666')
    parser.add_argument('--credentials', required=True)
    parser.add_argument('--media', required=True, help='new scripts/batch-round/ingest/media.py')
    parser.add_argument('--scratch', required=True, help='private media root (generated phase dirs)')
    parser.add_argument('--facts', required=True, help='per-phase generator facts')
    parser.add_argument('--marker', required=True, help='JSONL marker for the load helper')
    parser.add_argument('--control', help='atomic JSON control file for load.ts --phases')
    parser.add_argument('--acknowledge-300k', action='store_true')
    parser.add_argument('--appdata', help='stopped/current appdata path for the whole-fixture byte accounting')
    parser.add_argument('--generation', choices=['template', 'fresh'], default='template')
    parser.add_argument('--total', type=int, default=300000)
    parser.add_argument('--phase-size', type=int, default=50000)
    parser.add_argument('--scan-mode', default='', choices=['', 'incremental', 'full', 'integrity'])
    parser.add_argument('--scan-timeout', type=float, default=3600)
    parser.add_argument('--resident-rounds', type=int, default=20)
    parser.add_argument('--baseline-seconds', type=float, default=180)
    parser.add_argument('--after-seconds', type=float, default=180)
    parser.add_argument('--library-movies')
    parser.add_argument('--library-tv')
    parser.add_argument('--library-music')
    parser.add_argument('--execute', action='store_true')
    parser.add_argument('--tiny', action='store_true')
    args = parser.parse_args()
    if args.total < 1 or args.phase_size < 1 or args.resident_rounds < 1:
        refuse('total, phase-size and resident-rounds must be positive')
    if args.tiny and args.total > 100:
        refuse('--tiny is the 30/100 rehearsal only')
    if args.execute and args.total > 100:
        if not (args.acknowledge_300k and args.total == 300000):
            refuse('execution above 100 requires --acknowledge-300k and --total 300000')
    phases = (args.total + args.phase_size - 1) // args.phase_size
    resident_points = {args.phase_size, args.phase_size * 3, args.total}
    plan = {'state': 'PLAN', 'base': args.base, 'total': args.total, 'phaseSize': args.phase_size,
            'phases': phases, 'residentReadsAt': sorted(point for point in resident_points if point <= args.total),
            'scanMode': args.scan_mode or 'server default (recorded)', 'execute': False,
            'marker': args.marker, 'stopBytes': STOP_BYTES, 'hardBytes': HARD_BYTES,
            'markerSchema': {'kind': 'p2-65-ingest', 'version': 1, 'events':
                             ['phase-start', 'phase-end', 'resident-read', 'fixture-end']}}
    if not args.execute:
        print(json.dumps(plan))
        return
    if 'CI' in os.environ or 'GITHUB_ACTIONS' in os.environ:
        refuse('live ingest orchestration is prohibited in CI')
    if not args.control:
        refuse('--control is required on execute so load.ts can read phases atomically')
    credentials = json.loads(private_file(args.credentials).read_text())
    marker_path = pathlib.Path(args.marker)
    private_dir(marker_path.parent, create=True)
    scratch = private_dir(args.scratch, create=True)
    if args.total > 100:
        from quota import require_quota
        if not args.appdata:
            refuse('heavy ingest requires --appdata inside the quota')
        require_quota(scratch)
        require_quota(args.appdata)
        if os.stat(scratch).st_dev != os.stat(args.appdata).st_dev:
            refuse('media and appdata must share the same kernel quota')
    client = sign_in(args.base, credentials['username'], credentials['password'],
                     credentials.get('installationId') or os.urandom(18).hex())
    libs = resolve_libraries(client, {'movie': args.library_movies, 'tv': args.library_tv,
                                      'music': args.library_music})
    pivots = {kind: choose_pivot(client, libs[kind], kind) for kind in LIB_KINDS}
    total_files = 0
    accepted_before = 0
    started = time.monotonic()
    write_control(args.control, 0, accepted_before, 'baseline')
    time.sleep(args.baseline_seconds)
    ingest_started = time.monotonic()
    for phase in range(1, phases + 1):
        target = min(args.total, phase * args.phase_size)
        write_control(args.control, phase, accepted_before, 'phase-start')
        append_marker(marker_path, {'kind': 'p2-65-ingest', 'version': 1,
                                    'at': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()),
                                    'event': 'phase-start', 'phase': phase, 'target': args.total,
                                    'totalFiles': total_files, 'accepted': accepted_before,
                                    'ingestPerSecond': None, 'writerMsPerItem': None,
                                    'rssBytes': None, 'unavailable': []})
        before, before_error = diagnostics(client)
        phase_started = time.monotonic()
        facts = generate_phase(args.media, scratch, args.total, phase, args.phase_size, args.facts, args.generation)
        total_files = int(facts.get('files') or 0)
        generation_seconds = time.monotonic() - phase_started
        scan_started = time.monotonic()
        jobs = {}
        for kind in LIB_KINDS:
            jobs[kind] = scan(client, libs[kind], args.scan_mode)
        unavailable = []
        if before_error:
            unavailable.append(before_error)
        for kind in LIB_KINDS:
            sources, error = wait_terminal(client, libs[kind], args.scan_timeout)
            if error:
                unavailable.append(f'{kind}:{error}')
        counts, count_errors = {}, {}
        for kind in LIB_KINDS:
            accepted, error = accepted_count(client, libs[kind], pivots[kind])
            if accepted is None:
                accepted, error = paginated_count(client, libs[kind], pivots[kind])
            counts[kind] = accepted
            if error:
                count_errors[kind] = error
                unavailable.append(f'{kind}:{error}')
        accepted = sum(value for value in counts.values() if value is not None)
        elapsed = max(1e-6, time.monotonic() - phase_started)
        diagnostics_after, diagnostics_error = diagnostics(client)
        writer = None
        writer_share = None
        rss = None
        if diagnostics_error:
            unavailable.append(diagnostics_error)
        elif diagnostics_after:
            held = (diagnostics_after.get('writeGate') or {}).get('heldMillis')
            prior = ((before or {}).get('writeGate') or {}).get('heldMillis')
            accepted_delta = max(0, accepted - accepted_before)
            window_ms = max(1.0, (time.monotonic() - phase_started) * 1000)
            writer_share = None
            if held is None or prior is None:
                unavailable.append('writeGate.heldMillis missing')
            else:
                held_delta = max(0, int(held) - int(prior))
                writer_share = round(held_delta / window_ms, 4)
                if accepted_delta:
                    writer = round(held_delta / accepted_delta, 3)
                else:
                    unavailable.append('no accepted delta for writer ms/item')
            heap = (diagnostics_after.get('process') or {}).get('heapAllocBytes')
            rss = heap
            if rss is None:
                unavailable.append('process.heapAllocBytes missing')
            if (diagnostics_after.get('writePlans') or {}).get('overruns') is None:
                unavailable.append('writePlans.overruns missing')
        ingest_rate = round(max(0, accepted - accepted_before) / elapsed, 3) if accepted >= accepted_before else None
        media_bytes = tree_bytes(scratch)
        appdata_bytes = tree_bytes(pathlib.Path(args.appdata)) if args.appdata else None
        wal_path = pathlib.Path(args.appdata) / 'Database' / 'server.sqlite-wal' if args.appdata else None
        wal_bytes = wal_path.stat().st_size if wal_path and wal_path.exists() else None
        total_bytes = (media_bytes or 0) + (appdata_bytes or 0)
        physical_basis = 'tiny media+appdata trees; runtime temp excluded'
        if args.total > 100:
            fs = os.statvfs(scratch)
            total_bytes = (fs.f_blocks-fs.f_bfree)*fs.f_frsize
            physical_basis = 'whole quota allocated blocks, includes baseline/runtime temp/filesystem metadata'
        if appdata_bytes is None:
            unavailable.append('appdata physical bytes not supplied (--appdata)')
        if wal_bytes is None:
            unavailable.append('WAL physical bytes not readable')
        if total_bytes >= STOP_BYTES:
            unavailable.append('whole fixture reached the 7 GiB stop')
        record = {'kind': 'p2-65-ingest', 'version': 1,
                  'at': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()),
                  'event': 'phase-end', 'phase': phase, 'target': args.total,
                  'totalFiles': total_files, 'accepted': accepted, 'perKind': counts,
                  'ingestPerSecond': ingest_rate, 'writerMsPerItem': writer,
                  'bucketElapsedSeconds': round(elapsed, 3), 'generationSeconds': round(generation_seconds, 3),
                  'scanSeconds': round(time.monotonic()-scan_started, 3),
                  'writerShare': writer_share, 'heapAllocBytes': rss, 'memoryBasis': 'owner diagnostics process.heapAllocBytes; not RSS',
                  'writerBasis': 'all writer classes: writeGate.heldMillis delta, includes concurrent foreground work',
                  'generation': args.generation, 'pivots': pivots,
                  'mediaPhysicalBytes': media_bytes, 'appdataPhysicalBytes': appdata_bytes,
                  'walBytes': wal_bytes, 'totalPhysicalBytes': total_bytes, 'physicalBasis': physical_basis,
                  'scanMode': args.scan_mode or 'default', 'jobs': {kind: (jobs[kind] or {}).get('id') for kind in LIB_KINDS},
                  'unavailable': unavailable}
        append_marker(marker_path, record)
        write_control(args.control, phase, accepted, 'phase-end')
        expected_counts = {'tv': total_files // 2, 'music': total_files // 5}
        expected_counts['movie'] = total_files - sum(expected_counts.values())
        if accepted != total_files or counts != expected_counts:
            append_marker(marker_path, {'kind': 'p2-65-ingest', 'version': 1,
                                        'at': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()),
                                        'event': 'fixture-end', 'phase': phase, 'target': args.total,
                                        'totalFiles': total_files, 'accepted': accepted,
                                        'ingestPerSecond': ingest_rate, 'writerMsPerItem': writer,
                                        'rssBytes': rss, 'state': 'COUNT_MIX_MISMATCH',
                                        'unavailable': unavailable + ['accepted count/mix differs from real files']})
            refuse(f'accepted count/mix differs from {total_files} real files on disk', 3)
        accepted_before = accepted
        if target in resident_points:
            samples = operation_stats(read_sample(client, libs, pivots, args.resident_rounds))
            write_control(args.control, phase, accepted, 'resident-read')
            time.sleep(1)
            append_marker(marker_path, {'kind': 'p2-65-ingest', 'version': 1,
                                        'at': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()),
                                        'event': 'resident-read', 'phase': phase, 'target': args.total,
                                        'totalFiles': target, 'accepted': accepted,
                                        'ingestPerSecond': None, 'writerMsPerItem': None,
                                        'heapAllocBytes': rss, 'memoryBasis': 'heapAllocBytes; not RSS',
                                        'readP50Ms': samples['overall']['p50Ms'],
                                        'readP99Ms': samples['overall']['p99Ms'],
                                        'readSamples': samples['overall']['samples'],
                                        'reads': samples,
                                        'unavailable': []})
    ingest_elapsed = time.monotonic()-ingest_started
    convergence = reconcile(client, libs, pivots, counts)
    append_marker(marker_path, {'kind': 'p2-65-ingest', 'version': 1, 'event': 'convergence',
                               'at': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()), **convergence})
    if not convergence['passed']:
        refuse('post-ingest counts/facets/availability did not converge; see marker', 3)
    write_control(args.control, phases, accepted_before, 'fixture-end')
    time.sleep(args.after_seconds)
    append_marker(marker_path, {'kind': 'p2-65-ingest', 'version': 1,
                                'at': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()),
                                'event': 'fixture-end', 'phase': phases, 'target': args.total,
                                'totalFiles': total_files, 'accepted': accepted_before,
                                'ingestPerSecond': None, 'writerMsPerItem': None, 'rssBytes': None,
                                'elapsedSeconds': round(time.monotonic() - started, 3),
                                'ingestElapsedSeconds': round(ingest_elapsed, 3),
                                'unavailable': []})
    print(json.dumps({'state': 'COMPLETE', 'accepted': accepted_before, 'totalFiles': total_files,
                      'marker': str(marker_path), 'elapsedSeconds': round(time.monotonic() - started, 3)}))


if __name__ == '__main__':
    main()
