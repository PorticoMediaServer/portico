#!/usr/bin/env python3
"""Scratch load harness (server load review). N users browse, open details, search,
ask for playback options and open books, idle and then during a scan plus analysis.
Usage: scratch-load.py --scratch DIR --binary BIN [--films 400 --new 1500 --books 150 --secs 40 --users 10,50,100]"""
import argparse, importlib.util, json, random, shutil, subprocess, threading, time, uuid, zipfile, urllib.parse
from pathlib import Path
spec = importlib.util.spec_from_file_location('e2e', Path(__file__).with_name('e2e-server.py')); e2e = importlib.util.module_from_spec(spec); spec.loader.exec_module(e2e)
p = argparse.ArgumentParser(); p.add_argument('--scratch', type=Path, required=True); p.add_argument('--binary', type=Path, required=True)
for k, v in [('films', 400), ('new', 1500), ('books', 150), ('secs', 40)]: p.add_argument('--'+k, type=int, default=v)
p.add_argument('--users', default='10,50,100'); A = p.parse_args()
D = A.scratch.resolve()/('load-'+time.strftime('%H%M%S')); M = D/'media'; M.mkdir(parents=True)
base = D/'base.mp4'; e2e.run_command(['ffmpeg','-loglevel','error','-f','lavfi','-i','testsrc2=size=320x180:rate=24','-f','lavfi','-i','sine=frequency=440','-t','6','-c:v','libx264','-preset','ultrafast','-c:a','aac','-movflags','+faststart',str(base)])
def films(lib, n, off=0):
    for i in range(off, off+n): f = M/lib/f'Film {i} ({1950+i%70})'/f'Film {i} ({1950+i%70}).mp4'; f.parent.mkdir(parents=True); shutil.copyfile(base, f)
def epub(path, i):
    path.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(path, 'w') as z:
        z.writestr(zipfile.ZipInfo('mimetype'), 'application/epub+zip')
        z.writestr('META-INF/container.xml', '<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/c.opf" media-type="application/oebps-package+xml"/></rootfiles></container>')
        z.writestr('OEBPS/c.opf', f'<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="id">urn:uuid:{uuid.uuid4()}</dc:identifier><dc:title>Book {i}</dc:title><dc:creator>Author {i%20}</dc:creator><dc:language>en</dc:language><meta property="dcterms:modified">2026-01-01T00:00:00Z</meta></metadata><manifest><item id="n" href="n.xhtml" media-type="application/xhtml+xml" properties="nav"/><item id="c" href="c.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c"/></spine></package>')
        z.writestr('OEBPS/n.xhtml', '<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>n</title></head><body><nav epub:type="toc"><ol><li><a href="c.xhtml">One</a></li></ol></nav></body></html>')
        z.writestr('OEBPS/c.xhtml', '<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>c</title></head><body>' + '<p>Words of the story go on here. </p>'*400 + '</body></html>')
films('Films', A.films); films('New', A.new, 100000); [epub(M/'Books'/f'Author {i%20}'/f'Book {i}.epub', i) for i in range(A.books)]
_call = e2e.API.call
def call401(self, method, path, body=None, expected=(200,), headers=None, raw=False):  # access tokens expire: sign in again once
    r = _call(self, method, path, body, (*expected, 401), headers, raw)
    if self.timings[-1]['status'] != 401 or path == '/v1/direct/sign-in': return _call(self, method, path, body, expected, headers, raw) if self.timings[-1]['status'] == 401 else r
    self.token = _call(self, 'POST', '/v1/direct/sign-in', {'username': 'owner', 'password': X.password})[0]['session']['accessToken']
    return _call(self, method, path, body, expected, headers, raw)
e2e.API.call = call401
X = e2e.Exercise(D, A.binary.resolve()); X.start()
try: X.setup()
except KeyError: pass  # e2e-server.py's final 'ready' assert is stale against /v1/setup/state
except BaseException: X.stop(); raise
a = X.api; out = {'phases': [], 'helpers': {}}
diag = lambda: a.get('/v1/admin/diagnostics/concurrency')
def lib(name, kind):
    l = a.post('/v1/libraries', {'name': name, 'kind': kind, 'path': str(M/name), 'metadataAgent': 'local'}, (201,)); a.post('/v1/libraries/'+l['id']+'/scans', expected=(202,)); return l['id']
def quiet(cap=1500):  # analysis settled: no new helper process for 20 s (a watcher helper stays resident)
    t0, calm, last = time.monotonic(), 0, -1
    while time.monotonic()-t0 < cap and calm < 20:
        n = diag()['storageHelpers']['admitted']; calm = calm+1 if n == last else 0; last = n; time.sleep(1)
    return round(time.monotonic()-t0)
def ids(lid, pivot):
    got, cur = [], ''
    for _ in range(40):
        page = a.post('/v1/libraries/'+lid+'/browse', {'pivot': pivot, 'limit': 100, **({'cursor': cur} if cur else {})}); got += [e['id'] for e in page['entries']]; cur = page['pageInfo'].get('nextCursor')
        if not cur: break
    return got
FL, BL = lib('Films', 'movie'), lib('Books', 'book'); out['settleSecs'] = quiet()
movies, books = ids(FL, 'movies'), ids(BL, 'books'); src = {b: a.get('/v1/items/'+b+'/reading/versions').get('chosenSourceId') for b in books[:60]}
out['catalogue'] = {'movies': len(movies), 'books': len(books), 'readable': sum(1 for v in src.values() if v)}
def ops(u):
    m, b = random.choice(movies), random.choice([k for k, v in src.items() if v] or [None])
    return {'browse': ('POST', '/v1/libraries/'+FL+'/browse', {'pivot': 'movies', 'limit': 50}),
            'detail': ('GET', '/v1/items/'+m+'/detail', None), 'search': ('GET', '/v1/search?q='+urllib.parse.quote('Film %d' % random.randrange(400)), None),
            'options': ('GET', '/v1/items/'+m+'/playback-options', None), 'read': ('POST', '/v1/reading/sessions', {'itemId': b, 'sourceId': src.get(b)}) if b else None}
WEIGHTS = ['browse']*30 + ['detail']*25 + ['search']*10 + ['options']*20 + ['read']*15 if out['catalogue']['readable'] else ['browse']*35 + ['detail']*30 + ['search']*10 + ['options']*25
def call(u, op):
    req = ops(u)[op]; t = time.monotonic()
    try:
        body, h = u.call(req[0], req[1], req[2], expected=range(100, 600)); s = u.timings[-1]['status']; ms = round((time.monotonic()-t)*1000, 1)
        if s >= 300: s = f"{s}:{(body or {}).get('error', {}).get('code')}"
        elif op == 'read': u.call('DELETE', '/v1/reading/sessions/'+body['sessionId'], expected=range(100, 600))
    except Exception as err: s, ms = ('timeout' if 'timed out' in str(err) else 'error'), round((time.monotonic()-t)*1000, 1)
    return op, ms, s
for op in [k for k in ['browse', 'detail', 'search', 'options', 'read'] if ops(a)[k]]:  # helper processes per request, idle server
    h0 = diag()['storageHelpers']['admitted']; [call(a, op) for _ in range(5)]; out['helpers'][op] = (diag()['storageHelpers']['admitted']-h0)/5
users = []
for i in range(max(map(int, A.users.split(',')))):
    u = e2e.API(X.base, installation=str(uuid.uuid4())); u.token = u.post('/v1/direct/sign-in', {'username': 'owner', 'password': X.password})['session']['accessToken']; users.append(u)
def phase(n, label):
    stop, rows, d0 = threading.Event(), [], diag()
    def run(u):
        while not stop.is_set(): rows.append(call(u, random.choice(WEIGHTS)) + (time.monotonic(),)); stop.wait(random.uniform(.5, 1.5))
    ts = [threading.Thread(target=run, args=(u,), daemon=True) for u in users[:n]]; [t.start() for t in ts]
    if label == 'busy' and n == max(map(int, A.users.split(','))):
        time.sleep(A.secs/2)
        for prof in ['block', 'mutex', 'goroutine']:
            (D/f'{prof}-{n}.pb.gz').write_bytes(a.call('GET', '/v1/admin/diagnostics/pprof/'+prof, raw=True)[0])
        (D/f'diag-busy-{n}.json').write_text(json.dumps(diag(), indent=1))
    time.sleep(A.secs if label != 'busy' or n != max(map(int, A.users.split(','))) else A.secs/2); stop.set(); [t.join(35) for t in ts]; d1 = diag()
    res = {'users': n, 'label': label, 'requests': len(rows), 'helpersStarted': d1['storageHelpers']['admitted']-d0['storageHelpers']['admitted'],
           'gate': {k: d1['writeGate'].get(k, 0)-(0 if k == 'maxHeldMillis' else d0['writeGate'].get(k, 0)) for k in ('maxHeldMillis', 'queueWaitMillis', 'heldMillis', 'acquired')}, 'poolWaitMs': d1['pool']['waitMillis']-d0['pool']['waitMillis'], 'snapWaitMs': d1['readSnapshots']['waitMillis']-d0['readSnapshots']['waitMillis'], 'bgActive': d1['storageHelpers']['backgroundActive']+d1['storageHelpers']['scanProbeActive'], 'ops': {}}
    for op in set(r[0] for r in rows):
        ms = sorted(r[1] for r in rows if r[0] == op); st = [r[2] for r in rows if r[0] == op]; q = lambda f: ms[min(len(ms)-1, int(f*len(ms)))]
        res['ops'][op] = {'n': len(ms), 'p50': q(.5), 'p95': q(.95), 'p99': q(.99), 'max': ms[-1], '503': sum(1 for s in st if str(s).startswith('503')), 'non2xx': sum(1 for s in st if not (isinstance(s, int) and s < 300)), 'statuses': sorted(set(map(str, st)))}
    out['phases'].append(res); print(json.dumps(res), flush=True); (D/'load.json').write_text(json.dumps(out, indent=1))
try:
    for n in map(int, A.users.split(',')): phase(n, 'idle')
    NL = lib('New', 'movie'); a.post('/v1/libraries/'+FL+'/scans?mode=full', expected=(202,)); time.sleep(5)
    for n in map(int, A.users.split(',')): phase(n, 'busy')
    out['finalDiag'] = diag()
finally:
    (D/'load.json').write_text(json.dumps(out, indent=1)); X.stop(); print('Evidence:', D)
