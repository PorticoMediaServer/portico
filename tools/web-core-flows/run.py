#!/usr/bin/env python3
"""Disposable same-origin stack for the web core-flow walk (Search, Settings, Read, Live).

Builds a release-shaped server process against throwaway state, serves a built
web bundle from that same origin, provisions a local owner, creates one
book library with one lawful EPUB fixture, scans it, then hands the origin to
tools/web-core-flows/flows.mjs. Nothing here touches Hosted, the demo server or
any real credential.

Usage:
  python3 tools/web-core-flows/run.py --scratch DIR --web-dir web/dist \
      --output DIR --playwright /path/to/playwright-core/index.mjs

The scratch tree holds a throwaway owner password and must not be published.
"""
import argparse
import base64
import importlib.util
import json
import os
import secrets
import shutil
import signal
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
BOOK_TITLE = 'Example Book'
BOOK_AUTHOR = 'Example Author'
LIBRARY_NAME = 'Books'


def port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


def e2e_module():
    spec = importlib.util.spec_from_file_location('e2e_server', ROOT / 'scripts/e2e-server.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def write_epub(target):
    """A minimal, lawful EPUB 3 document: two short paragraphs of fixture prose."""
    target.parent.mkdir(parents=True, exist_ok=True)
    container = '''<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>
'''
    opf = f'''<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="bookid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="bookid">urn:uuid:7d0e2b1a-0000-4000-8000-000000000001</dc:identifier>
    <dc:title>{BOOK_TITLE}</dc:title>
    <dc:creator>{BOOK_AUTHOR}</dc:creator>
    <dc:language>en</dc:language>
    <meta property="dcterms:modified">2026-10-24T00:00:00Z</meta>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="chapter1" href="chapter1.xhtml" media-type="application/xhtml+xml"/>
    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
  </manifest>
  <spine toc="ncx"><itemref idref="chapter1"/></spine>
</package>
'''
    nav = '''<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" xml:lang="en">
<head><title>Contents</title></head>
<body><nav epub:type="toc" id="toc"><h1>Contents</h1><ol><li><a href="chapter1.xhtml">Chapter One</a></li></ol></nav></body>
</html>
'''
    chapter = '''<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="en">
<head><title>Chapter One</title></head>
<body>
<h1>Chapter One</h1>
<p>The lamp on the desk had been lit since morning, and the room had grown used to it.</p>
<p>She read the page twice, then set it down and listened to the house settle.</p>
</body>
</html>
'''
    ncx = f'''<?xml version="1.0" encoding="UTF-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
<head><meta name="dtb:uid" content="urn:uuid:7d0e2b1a-0000-4000-8000-000000000001"/></head>
<docTitle><text>{BOOK_TITLE}</text></docTitle>
<navMap><navPoint id="n1" playOrder="1"><navLabel><text>Chapter One</text></navLabel><content src="chapter1.xhtml"/></navPoint></navMap>
</ncx>
'''
    with zipfile.ZipFile(target, 'w') as book:
        # The mimetype entry must be first and stored uncompressed.
        book.writestr(zipfile.ZipInfo('mimetype'), 'application/epub+zip', compress_type=zipfile.ZIP_STORED)
        for name, body in [('META-INF/container.xml', container), ('OEBPS/content.opf', opf),
                           ('OEBPS/nav.xhtml', nav), ('OEBPS/chapter1.xhtml', chapter), ('OEBPS/toc.ncx', ncx)]:
            book.writestr(name, body, compress_type=zipfile.ZIP_DEFLATED)


def write_pdf(target):
    """A minimal, lawful one-page PDF: a single line of fixture text."""
    target.parent.mkdir(parents=True, exist_ok=True)
    objects = [
        b"<< /Type /Catalog /Pages 2 0 R >>",
        b"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
        b"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
    ]
    stream = b"BT /F1 24 Tf 72 700 Td (Example Book PDF) Tj ET"
    objects.append(b"<< /Length " + str(len(stream)).encode() + b" >>\nstream\n" + stream + b"\nendstream")
    objects.append(b"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
    out = bytearray(b"%PDF-1.4\n")
    offsets = []
    for number, body in enumerate(objects, 1):
        offsets.append(len(out))
        out += str(number).encode() + b" 0 obj\n" + body + b"\nendobj\n"
    xref = len(out)
    out += b"xref\n0 " + str(len(objects) + 1).encode() + b"\n0000000000 65535 f \n"
    for offset in offsets:
        out += ("%010d 00000 n \n" % offset).encode()
    out += (b"trailer\n<< /Size " + str(len(objects) + 1).encode() + b" /Root 1 0 R >>\nstartxref\n"
            + str(xref).encode() + b"\n%%EOF\n")
    target.write_bytes(bytes(out))


def stripe_png(width=32, height=48):
    """A real RGB PNG: IHDR, one zlib-compressed IDAT and IEND, each with a
    genuine CRC. Horizontal red/green/blue stripes, one pixel per row band."""
    import struct
    import zlib

    def chunk(kind, data):
        return struct.pack('>I', len(data)) + kind + data + struct.pack('>I', zlib.crc32(kind + data) & 0xFFFFFFFF)

    raw = bytearray()
    for y in range(height):
        raw.append(0)  # filter: none
        colour = ((255, 0, 0), (0, 255, 0), (0, 0, 255))[(y // 4) % 3]
        raw.extend(bytes(colour) * width)
    ihdr = struct.pack('>IIBBBBB', width, height, 8, 2, 0, 0, 0)
    return b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', ihdr) + chunk(b'IDAT', zlib.compress(bytes(raw), 9)) + chunk(b'IEND', b'')


def write_cbz(target):
    """A lawful one-page CBZ: a real 1x1 PNG plus ComicInfo, nothing else."""
    target.parent.mkdir(parents=True, exist_ok=True)
    # Deliberately retained negative control: this 1x1 PNG has a corrupt IDAT
    # CRC (verified locally), so the browser decoder must refuse it. Kept as-is.
    png = base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR42mNgAAIAAAUAAarVyFEAAAAASUVORK5CYII=')
    info = b'<ComicInfo><Title>Example Comic</Title><Writer>Example Author</Writer></ComicInfo>'
    with zipfile.ZipFile(target, 'w') as comic:
        comic.writestr('page-01.png', png, compress_type=zipfile.ZIP_DEFLATED)
        comic.writestr('ComicInfo.xml', info, compress_type=zipfile.ZIP_DEFLATED)
    # Positive control: the same CBZ shape with a real 32x48 RGB PNG.
    stripe = stripe_png()
    stripe_info = b'<ComicInfo><Title>Stripe Comic</Title><Writer>Example Author</Writer></ComicInfo>'
    with zipfile.ZipFile(target.parent / 'Stripe Comic.cbz', 'w') as comic:
        comic.writestr('page-01.png', stripe, compress_type=zipfile.ZIP_DEFLATED)
        comic.writestr('ComicInfo.xml', stripe_info, compress_type=zipfile.ZIP_DEFLATED)


def write_hostile_epub(target):
    """A book whose markup would run a script and reach the network if the
    reader's sanitisation or the book CSP were missing. Only the plain
    paragraph is ever expected on screen."""
    target.parent.mkdir(parents=True, exist_ok=True)
    container = """<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>
"""
    opf = """<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="hostile">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="hostile">urn:uuid:7d0e2b1a-0000-4000-8000-000000000002</dc:identifier>
    <dc:title>Hostile Notes</dc:title>
    <dc:creator>Example Author</dc:creator>
    <dc:language>en</dc:language>
    <meta property="dcterms:modified">2026-10-24T00:00:00Z</meta>
  </metadata>
  <manifest>
    <item id="api" href="api.xhtml" media-type="application/xhtml+xml"/>
    <item id="chapter1" href="chapter1.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="chapter1"/></spine>
</package>
"""
    chapter = """<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="en">
<head><title>Chapter One</title>
<script>fetch("http://127.0.0.1:9/portico-epub-egress").catch(function(){});</script>
<script src="api.xhtml"></script>
</head>
<body onload="fetch('http://127.0.0.1:9/portico-epub-egress-onload').catch(function(){})">
<h1>Hostile Notes</h1>
<p>This paragraph is the only thing a reader may draw.</p>
<img src="http://127.0.0.1:9/portico-epub-egress-image.png" alt=""/>
<iframe src="http://127.0.0.1:9/portico-epub-egress-frame"></iframe>
<a href="http://127.0.0.1:9/portico-epub-egress-link">Link</a>
</body>
</html>
"""
    api = """<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="en"><head><title>API</title></head>
<body><p>book script ran</p></body></html>
"""
    with zipfile.ZipFile(target, 'w') as book:
        book.writestr(zipfile.ZipInfo('mimetype'), 'application/epub+zip', compress_type=zipfile.ZIP_STORED)
        for name, body in [('META-INF/container.xml', container), ('OEBPS/content.opf', opf),
                           ('OEBPS/chapter1.xhtml', chapter), ('OEBPS/api.xhtml', api)]:
            book.writestr(name, body, compress_type=zipfile.ZIP_DEFLATED)


def wait(fn, timeout=180):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        value = fn()
        if value:
            return value
        time.sleep(.3)
    raise AssertionError('timed out waiting for the disposable stack')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--scratch', type=Path, required=True)
    parser.add_argument('--web-dir', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--binary', type=Path)
    parser.add_argument('--playwright', required=True)
    parser.add_argument('--chromium', default='/usr/bin/chromium')
    args = parser.parse_args()
    scratch, output = args.scratch.resolve(), args.output.resolve()
    if scratch == ROOT or ROOT in scratch.parents:
        parser.error('scratch must live outside the repository')
    if not (args.web_dir / 'index.html').is_file():
        parser.error('web-dir has no index.html; build portico-web first')
    scratch.mkdir(parents=True, exist_ok=True, mode=0o700)
    output.mkdir(parents=True, exist_ok=True)
    os.umask(0o077)
    run = scratch / 'stack'
    run.mkdir(mode=0o700, exist_ok=True)
    e2e = e2e_module()
    binary = args.binary or (scratch / 'portico-server')
    if not args.binary:
        print('building server', flush=True)
        built = subprocess.run(['go', 'build', '-trimpath', '-buildvcs=false', '-o', str(binary), './cmd/server'],
                               cwd=ROOT / 'server', env={**os.environ, 'GOFLAGS': '-p=2'},
                               stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        if built.returncode:
            sys.stderr.write(built.stdout.decode(errors='replace')[-3000:])
            return built.returncode
    media = run / 'media'
    write_epub(media / LIBRARY_NAME / BOOK_AUTHOR / (BOOK_TITLE + '.epub'))
    write_pdf(media / LIBRARY_NAME / BOOK_AUTHOR / 'Reference Sheet.pdf')
    write_hostile_epub(media / LIBRARY_NAME / BOOK_AUTHOR / 'Hostile Notes.epub')
    write_cbz(media / 'Comics' / BOOK_AUTHOR / 'Example Comic.cbz')
    (media / 'Extra').mkdir(parents=True, exist_ok=True)
    ffmpeg = shutil.which('ffmpeg') or '/opt/portico-ffmpeg/bin/ffmpeg'
    movie = media / 'Movies' / 'Example Movie (2019)' / 'Example Movie (2019).mp4'
    movie.parent.mkdir(parents=True, exist_ok=True)
    made = subprocess.run([ffmpeg, '-hide_banner', '-loglevel', 'error', '-f', 'lavfi', '-i', 'testsrc2=size=320x180:rate=24',
                           '-f', 'lavfi', '-i', 'sine=frequency=440:sample_rate=48000', '-t', '3', '-c:v', 'libx264',
                           '-threads', '1', '-preset', 'ultrafast', '-pix_fmt', 'yuv420p', '-c:a', 'aac',
                           '-movflags', '+faststart', str(movie)], stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    if made.returncode:
        raise SystemExit('could not generate the movie fixture: ' + made.stdout.decode(errors='replace')[-1500:])
    base = f'http://127.0.0.1:{port()}'
    env = {key: value for key, value in os.environ.items() if not key.startswith('PORTICO_')}
    env.update(PORTICO_DATA_DIR=str(run / 'state'), PORTICO_BIND=base.split('//')[1], PORTICO_DISCOVERY='0',
               PORTICO_WEB_DIR=str(args.web_dir.resolve()), PORTICO_REQUEST_STANDARD_FOLDERS='true')
    log = open(run / 'server.log', 'wb')
    process = subprocess.Popen([str(binary)], cwd=ROOT / 'server', env=env, stdout=log, stderr=subprocess.STDOUT,
                               start_new_session=True)
    api = e2e.API(base)
    summary = {'base': base, 'steps': []}
    try:
        def ready():
            if process.poll() is not None:
                raise RuntimeError('server exited; see scratch/stack/server.log')
            try:
                return api.get('/v1/system')
            except (OSError, AssertionError):
                return False
        wait(ready, 120)
        setup = e2e.API(base)
        bootstrap = setup.post('/v1/setup/browser', {}, headers={'Origin': base})
        password = secrets.token_urlsafe(24)
        created = setup.post('/v1/setup', {'setupToken': bootstrap['setupToken'], 'username': 'owner', 'password': password,
                                           'name': 'Flow Owner', 'authMode': 'local', 'interactive': True}, (201,))
        setup.token = created['accessToken']
        state = setup.get('/v1/setup/state')
        setup.post('/v1/setup/finish', {'revision': str(state['revision'])})
        signed = setup.post('/v1/direct/sign-in', {'username': 'owner', 'password': password})
        api.token = signed['session']['accessToken']
        api.call('PUT', '/v1/me/devices/current/capabilities', e2e.capabilities(), (204,))
        console = api.get('/v1/admin/console/settings')
        values = console['data']['requested']
        values.update({'updates.feedUrl': '', 'lanDiscoveryEnabled': False, 'library.missingTitles': 'never'})
        api.call('PATCH', '/v1/admin/console/settings',
                 {'expectedRevision': console['data']['revision'], 'idempotencyKey': 'flow-' + secrets.token_hex(6), 'values': values})
        library = api.post('/v1/libraries', {'name': LIBRARY_NAME, 'kind': 'book', 'path': str(media / LIBRARY_NAME),
                                             'metadataAgent': 'local'}, (201,))
        job = api.post('/v1/libraries/' + library['id'] + '/scans', expected=(202,))

        def scanned():
            current = api.get('/v1/admin/ingestion/jobs/' + job['id'])
            if current['status'] in ('failed', 'cancelled'):
                raise AssertionError('scan ' + current['status'])
            return current if current['status'] in ('complete', 'complete_with_warnings') else False
        summary['scan'] = wait(scanned, 240)['status']
        movies = api.post('/v1/libraries', {'name': 'Movies', 'kind': 'movie', 'path': str(media / 'Movies'),
                                            'metadataAgent': 'local'}, (201,))
        movieJob = api.post('/v1/libraries/' + movies['id'] + '/scans', expected=(202,))

        def movieScanned():
            current = api.get('/v1/admin/ingestion/jobs/' + movieJob['id'])
            if current['status'] in ('failed', 'cancelled'):
                raise AssertionError('movie scan ' + current['status'])
            return current if current['status'] in ('complete', 'complete_with_warnings') else False
        summary['movieScan'] = wait(movieScanned, 240)['status']
        comics = api.post('/v1/libraries', {'name': 'Comics', 'kind': 'comic', 'path': str(media / 'Comics'),
                                            'metadataAgent': 'local'}, (201,))
        comicJob = api.post('/v1/libraries/' + comics['id'] + '/scans', expected=(202,))

        def comicScanned():
            current = api.get('/v1/admin/ingestion/jobs/' + comicJob['id'])
            if current['status'] in ('failed', 'cancelled'):
                raise AssertionError('comic scan ' + current['status'])
            return current if current['status'] in ('complete', 'complete_with_warnings') else False
        summary['comicScan'] = wait(comicScanned, 240)['status']
        comicItems = api.get('/v1/reading/items?libraryId=' + urllib.parse.quote(comics['id']) + '&limit=20')
        summary['comicTitles'] = [item.get('title') for item in comicItems.get('items', [])]
        if not summary['comicTitles']:
            raise AssertionError('scanned comic library did not publish the comic')
        browse = api.post('/v1/libraries/' + movies['id'] + '/browse', {'pivot': 'movies', 'limit': 10})
        movieTitles = [entry.get('title') for entry in browse.get('entries', [])]
        summary['movieTitles'] = movieTitles
        if not any('Example Movie' in (title or '') for title in movieTitles):
            raise AssertionError('scanned movie library did not publish the movie: ' + repr(movieTitles))
        reading = api.get('/v1/reading/items?libraryId=' + urllib.parse.quote(library['id']) + '&limit=60')
        titles = [item.get('title') for item in reading.get('items', [])]
        summary['libraryTitles'] = titles
        for expected in (BOOK_TITLE, 'Reference Sheet', 'Hostile Notes'):
            if expected not in titles:
                raise AssertionError('scanned library did not publish ' + expected + ': ' + repr(titles))
        credentials = run / 'credentials.json'
        credentials.write_text(json.dumps({'username': 'owner', 'password': password, 'libraryName': LIBRARY_NAME,
                                           'libraryId': library['id'], 'bookTitle': BOOK_TITLE, 'bookText': 'Chapter One', 'pdfTitle': 'Reference Sheet', 'hostileTitle': 'Hostile Notes', 'addPath': str(media / 'Extra'), 'bookPath': str(media / LIBRARY_NAME), 'comicsLibraryId': comics['id'], 'comicTitle': 'Example Comic', 'stripeComicTitle': 'Stripe Comic',
                                           'searchTerm': 'Example'}), encoding='utf-8')
        walk = subprocess.run(['node', str(ROOT / 'tools/web-core-flows/flows.mjs')], cwd=ROOT,
                              env={**os.environ, 'PORTICO_FLOW_BASE': base, 'PORTICO_FLOW_CREDS': str(credentials),
                                   'PORTICO_FLOW_OUTPUT': str(output),
                                   'PORTICO_PLAYWRIGHT_MODULE': args.playwright,
                                   'PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH': args.chromium},
                              stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        (output / 'flows.log').write_text(walk.stdout.decode(errors='replace'))
        summary['flowsExit'] = walk.returncode
        print(walk.stdout.decode(errors='replace')[-4000:], flush=True)
        (output / 'stack-summary.json').write_text(json.dumps(summary, indent=2) + '\n')
        return walk.returncode
    except BaseException:
        log.flush()
        tail = (run / 'server.log').read_text(errors='replace')[-4000:]
        (output / 'server-tail.log').write_text(tail)
        sys.stderr.write('server log tail:\n' + tail + '\n')
        raise
    finally:
        try:
            os.killpg(process.pid, signal.SIGTERM)
            process.wait(timeout=30)
        except (ProcessLookupError, subprocess.TimeoutExpired):
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
        log.close()


if __name__ == '__main__':
    sys.exit(main())
