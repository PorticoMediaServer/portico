#!/usr/bin/env python3
"""Load a real Portico server with simulated people while a scan and analysis run.

Usage: python3 scripts/server-load.py --scratch /absolute/disposable/directory --binary PATH
       [--films 400] [--new 1500] [--books 150] [--secs 40] [--users 10,50,100] [--budgets FILE]

Each simulated person signs in from its own loopback address and loops with 0.5-1.5 s of
think time: browse 30%, title detail 25%, search 10%, playback options 20%, open and close
a book 15%. Idle phases run after the first import has settled; busy phases run while a new
library is first-imported and the first is fully rescanned. Results, with the server's
concurrency diagnostics and runtime profiles from the largest busy phase, go to load.json in
the run directory. With --budgets, any phase that misses its budget fails the run. Never
uses real credentials; the scratch directory contains secrets and must not be published.
"""
import argparse
import http.client
import importlib.util
import json
import os
import random
import shutil
import threading
import time
import urllib.parse
import urllib.request
import uuid
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('e2e', ROOT / 'scripts' / 'e2e-server.py')
e2e = importlib.util.module_from_spec(spec)
spec.loader.exec_module(e2e)

# Sign-in is limited per address, so each person connects from its own loopback address.
SOURCE = threading.local()


class SourceHandler(urllib.request.HTTPHandler):
    def http_open(self, req):
        ip = getattr(SOURCE, 'ip', None)
        if not ip:
            return self.do_open(http.client.HTTPConnection, req)
        return self.do_open(lambda host, **kw: http.client.HTTPConnection(host, source_address=(ip, 0), **kw), req)


_build_opener = urllib.request.build_opener
urllib.request.build_opener = lambda *handlers: _build_opener(*handlers, SourceHandler)
_call = e2e.API.call


def call_signing_in_again(self, method, path, body=None, expected=(200,), headers=None, raw=False):
    """Access tokens expire during a long run: sign in again once, then retry."""
    result = _call(self, method, path, body, (*expected, 401), headers, raw)
    if self.timings[-1]['status'] != 401 or path == '/v1/direct/sign-in' or not getattr(self, 'password', None):
        return _call(self, method, path, body, expected, headers, raw) if self.timings[-1]['status'] == 401 else result
    self.token = _call(self, 'POST', '/v1/direct/sign-in', {'username': 'owner', 'password': self.password})[0]['session']['accessToken']
    return _call(self, method, path, body, expected, headers, raw)


e2e.API.call = call_signing_in_again
WEIGHTS = ['browse'] * 30 + ['detail'] * 25 + ['search'] * 10 + ['options'] * 20 + ['read'] * 15


def epub(path, i):
    path.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(path, 'w') as z:
        z.writestr(zipfile.ZipInfo('mimetype'), 'application/epub+zip')
        z.writestr('META-INF/container.xml', '<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/c.opf" media-type="application/oebps-package+xml"/></rootfiles></container>')
        z.writestr('OEBPS/c.opf', f'<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="id">urn:uuid:{uuid.uuid4()}</dc:identifier><dc:title>Book {i}</dc:title><dc:creator>Author {i % 20}</dc:creator><dc:language>en</dc:language><meta property="dcterms:modified">2026-01-01T00:00:00Z</meta></metadata><manifest><item id="n" href="n.xhtml" media-type="application/xhtml+xml" properties="nav"/><item id="c" href="c.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c"/></spine></package>')
        z.writestr('OEBPS/n.xhtml', '<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>n</title></head><body><nav epub:type="toc"><ol><li><a href="c.xhtml">One</a></li></ol></nav></body></html>')
        z.writestr('OEBPS/c.xhtml', '<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>c</title></head><body>' + '<p>Words of the story go on here. </p>' * 400 + '</body></html>')


def quantile(values, q):
    return values[min(len(values) - 1, int(q * len(values)))]


class Load:
    def __init__(self, args):
        self.args = args
        self.directory = args.scratch.resolve() / ('load-' + time.strftime('%Y%m%d-%H%M%S'))
        self.media = self.directory / 'media'
        self.media.mkdir(parents=True, mode=0o700)
        self.out = {'phases': [], 'helpersPerRequest': {}}
        self.users = []

    def generate(self):
        base = self.directory / 'base.mp4'
        e2e.run_command(['ffmpeg', '-loglevel', 'error', '-f', 'lavfi', '-i', 'testsrc2=size=320x180:rate=24', '-f', 'lavfi', '-i', 'sine=frequency=440',
                         '-t', '6', '-c:v', 'libx264', '-preset', 'ultrafast', '-c:a', 'aac', '-movflags', '+faststart', str(base)])
        for library, count, offset in [('Films', self.args.films, 0), ('New', self.args.new, 100000)]:
            for i in range(offset, offset + count):
                target = self.media / library / f'Film {i} ({1950 + i % 70})' / f'Film {i} ({1950 + i % 70}).mp4'
                target.parent.mkdir(parents=True)
                shutil.copyfile(base, target)
        for i in range(self.args.books):
            epub(self.media / 'Books' / f'Author {i % 20}' / f'Book {i}.epub', i)

    def diagnostics(self):
        return self.api.get('/v1/admin/diagnostics/concurrency')

    def library(self, name, kind):
        created = self.api.post('/v1/libraries', {'name': name, 'kind': kind, 'path': str(self.media / name), 'metadataAgent': 'local'}, (201,))
        self.api.post('/v1/libraries/' + created['id'] + '/scans', expected=(202,))
        return created['id']

    def settle(self, cap=1500):
        """The first import has settled once no helper process has started for 20 s."""
        started, calm, last = time.monotonic(), 0, -1
        while time.monotonic() - started < cap and calm < 20:
            admitted = self.diagnostics()['storageHelpers']['admitted']
            calm, last = (calm + 1 if admitted == last else 0), admitted
            time.sleep(1)
        return round(time.monotonic() - started)

    def entries(self, library, pivot):
        found, cursor = [], ''
        for _ in range(40):
            page = self.api.post('/v1/libraries/' + library + '/browse', {'pivot': pivot, 'limit': 100, **({'cursor': cursor} if cursor else {})})
            found += [entry['id'] for entry in page['entries']]
            cursor = page['pageInfo'].get('nextCursor')
            if not cursor:
                break
        return found

    def request(self, op):
        movie = random.choice(self.movies)
        book = random.choice(self.readable) if self.readable else None
        return {
            'browse': ('POST', '/v1/libraries/' + self.films + '/browse', {'pivot': 'movies', 'limit': 50}),
            'detail': ('GET', '/v1/items/' + movie + '/detail', None),
            'search': ('GET', '/v1/search?q=' + urllib.parse.quote('Film %d' % random.randrange(max(1, self.args.films))), None),
            'options': ('GET', '/v1/items/' + movie + '/playback-options', None),
            'read': ('POST', '/v1/reading/sessions', {'itemId': book, 'sourceId': self.sources[book]}) if book else None,
        }[op]

    def call(self, user, op):
        req = self.request(op)
        started = time.monotonic()
        try:
            body, _ = user.call(req[0], req[1], req[2], expected=range(100, 600))
            status = user.timings[-1]['status']
            elapsed = round((time.monotonic() - started) * 1000, 1)
            if status >= 300:
                status = f"{status}:{(body or {}).get('error', {}).get('code')}"
            elif op == 'read':
                user.call('DELETE', '/v1/reading/sessions/' + body['sessionId'], expected=range(100, 600))
        except Exception as error:
            status, elapsed = ('timeout' if 'timed out' in str(error) else 'error'), round((time.monotonic() - started) * 1000, 1)
        return op, elapsed, status

    def phase(self, count, label, profile):
        stop, rows, before = threading.Event(), [], self.diagnostics()
        weights = WEIGHTS if self.readable else [w for w in WEIGHTS if w != 'read']

        def run(user):
            SOURCE.ip = user.ip
            while not stop.is_set():
                rows.append(self.call(user, random.choice(weights)))
                stop.wait(random.uniform(.5, 1.5))

        threads = [threading.Thread(target=run, args=(user,), daemon=True) for user in self.users[:count]]
        for thread in threads:
            thread.start()
        if profile:
            time.sleep(self.args.secs / 2)
            for name in ['block', 'mutex', 'goroutine']:
                (self.directory / f'{name}-{label}-{count}.pb.gz').write_bytes(self.api.call('GET', '/v1/admin/diagnostics/pprof/' + name, raw=True)[0])
            (self.directory / f'goroutines-{label}-{count}.txt').write_bytes(self.api.call('GET', '/v1/admin/diagnostics/pprof/goroutine?debug=2', raw=True)[0])
            (self.directory / f'cpu-{label}-{count}.pb.gz').write_bytes(self.api.call('GET', '/v1/admin/diagnostics/pprof/profile?seconds=8', raw=True)[0])
            (self.directory / f'diagnostics-{label}-{count}.json').write_text(json.dumps(self.diagnostics(), indent=1))
            time.sleep(max(0, self.args.secs / 2 - 8))
        else:
            time.sleep(self.args.secs)
        stop.set()
        for thread in threads:
            thread.join(35)
        after = self.diagnostics()
        result = {'users': count, 'label': label, 'requests': len(rows), 'perSecond': round(len(rows) / self.args.secs, 1),
                  'helpersStarted': after['storageHelpers']['admitted'] - before['storageHelpers']['admitted'],
                  'gateWaitMillis': after['writeGate']['queueWaitMillis'] - before['writeGate']['queueWaitMillis'],
                  'poolWaitMillis': after['pool']['waitMillis'] - before['pool']['waitMillis'],
                  'snapshotWaitMillis': after['readSnapshots']['waitMillis'] - before['readSnapshots']['waitMillis'],
                  'statementCacheMisses': after['reads']['statementCache']['misses'] - before['reads']['statementCache']['misses'], 'ops': {}}
        for op in sorted({row[0] for row in rows}):
            times = sorted(row[1] for row in rows if row[0] == op)
            statuses = [row[2] for row in rows if row[0] == op]
            result['ops'][op] = {'n': len(times), 'p50': quantile(times, .5), 'p95': quantile(times, .95), 'p99': quantile(times, .99), 'max': times[-1],
                                 'serverErrors': sum(1 for s in statuses if str(s)[:1] == '5' or s in ('timeout', 'error')),
                                 'statuses': sorted({str(s) for s in statuses})}
        self.out['phases'].append(result)
        print(json.dumps(result), flush=True)
        self.save()

    def save(self):
        (self.directory / 'load.json').write_text(json.dumps(self.out, indent=1))

    def run(self):
        self.generate()
        server = e2e.Exercise(self.directory, self.args.binary.resolve())
        server.start()
        try:
            server.setup()
            self.api = server.api
            self.api.password = server.password
            self.films, books = self.library('Films', 'movie'), self.library('Books', 'book')
            self.out['settleSeconds'] = self.settle()
            self.movies = self.entries(self.films, 'movies')
            book_ids = self.entries(books, 'books')
            self.sources = {b: self.api.get('/v1/items/' + b + '/reading/versions').get('chosenSourceId') for b in book_ids[:60]}
            self.readable = [b for b, source in self.sources.items() if source]
            self.out['catalogue'] = {'movies': len(self.movies), 'books': len(book_ids), 'readable': len(self.readable)}
            for op in ['browse', 'detail', 'search', 'options'] + (['read'] if self.readable else []):
                before = self.diagnostics()['storageHelpers']['admitted']
                for _ in range(5):
                    self.call(self.api, op)
                self.out['helpersPerRequest'][op] = (self.diagnostics()['storageHelpers']['admitted'] - before) / 5
            counts = [int(n) for n in self.args.users.split(',')]
            for i in range(max(counts)):
                user = e2e.API(server.base, installation=str(uuid.uuid4()))
                user.ip = SOURCE.ip = f'127.0.{1 + i // 200}.{10 + i % 200}'
                user.password = server.password
                user.token = user.post('/v1/direct/sign-in', {'username': 'owner', 'password': server.password})['session']['accessToken']
                self.users.append(user)
            SOURCE.ip = None
            for count in counts:
                self.phase(count, 'idle', profile=False)
            self.library('New', 'movie')
            self.api.post('/v1/libraries/' + self.films + '/scans?mode=full', expected=(202,))
            time.sleep(5)
            for count in counts:
                self.phase(count, 'busy', profile=count == max(counts))
            self.out['finalDiagnostics'] = self.diagnostics()
        finally:
            self.save()
            server.stop()
        return self.out


def check(out, budgets):
    """Each budget names users, a phase label (or "*") and per-operation p95 ceilings."""
    misses = []
    for budget in budgets:
        for phase in out['phases']:
            if phase['users'] != budget['users'] or budget['label'] not in ('*', phase['label']):
                continue
            for op, ceiling in budget.get('p95', {}).items():
                got = phase['ops'].get(op, {}).get('p95')
                if got is not None and got > ceiling:
                    misses.append(f"{phase['users']} users {phase['label']}: {op} p95 {got:.0f} ms over {ceiling} ms")
            allowed = budget.get('serverErrors', None)
            if allowed is not None:
                errors = sum(o['serverErrors'] for o in phase['ops'].values())
                if errors > allowed:
                    misses.append(f"{phase['users']} users {phase['label']}: {errors} server errors over {allowed}")
    return misses


def summary(out, misses):
    lines = ['| Users | Phase | req/s | ' + ' | '.join(f'{op} p50 / p95' for op in ['browse', 'detail', 'search', 'options', 'read']) + ' | 5xx |',
             '|---|---|---|' + '---|' * 6]
    for phase in out['phases']:
        cells = [f"{phase['ops'][op]['p50']:.0f} / {phase['ops'][op]['p95']:.0f} ms" if op in phase['ops'] else '—' for op in ['browse', 'detail', 'search', 'options', 'read']]
        errors = sum(o['serverErrors'] for o in phase['ops'].values())
        lines.append(f"| {phase['users']} | {phase['label']} | {phase['perSecond']} | " + ' | '.join(cells) + f' | {errors} |')
    lines.append('')
    lines += [f'- Budget missed: {miss}' for miss in misses] or ['- Every budget met.']
    return '\n'.join(lines)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--scratch', type=Path, required=True)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--films', type=int, default=400)
    parser.add_argument('--new', type=int, default=1500)
    parser.add_argument('--books', type=int, default=150)
    parser.add_argument('--secs', type=int, default=40)
    parser.add_argument('--users', default='10,50,100')
    parser.add_argument('--budgets', type=Path)
    args = parser.parse_args()
    scratch = args.scratch.resolve()
    if scratch == ROOT or ROOT in scratch.parents:
        parser.error('Scratch must be outside the repository (contains secrets and media)')
    os.umask(0o077)
    out = Load(args).run()
    misses = check(out, json.loads(args.budgets.read_text())) if args.budgets else []
    report = summary(out, misses)
    print(report, flush=True)
    if os.environ.get('GITHUB_STEP_SUMMARY'):
        with open(os.environ['GITHUB_STEP_SUMMARY'], 'a') as f:
            f.write('## Server under load\n\n' + report + '\n')
    raise SystemExit(1 if misses else 0)


if __name__ == '__main__':
    main()
