#!/usr/bin/env python3
"""Bounded fixture factory for the batch load campaign (O-041 / O-102).

Five bounded steps. Nothing is transplanted into a database, no schema is guessed, and no
authority is invented: every item, asset, source/root and grant below is created by the real
server through its own supported API, then read back.

  media       Generate N unique media filenames as hard links to real, probed source media in
              bounded directories (<= 1000 entries each), plus a real Reading EPUB. Accounts
              the physical bytes and refuses above the 7 GiB stop budget.
  bootstrap   Start the isolated current server on 127.0.0.1 with a private appdata directory,
              run the ordinary setup, create the library over the media root, wait for the
              real scan, capture the owner credential, stop the server, and read the stopped
              database's own schema/scan evidence back. Writes the preparation receipt.
  identities  Provision real member accounts and capture complete sign-in envelopes through
              the supported API; read each sign-in back with GET /v1/me.
  manifest    Assemble and validate the private load manifest, bound to the exact receipt.
  proposal    Print the replacement 300k real-scan test-round plan; it creates nothing.

Plan mode (no --execute) changes nothing. Every command refuses CI and any destination this
user does not own. A "full appstate" claim requires bootstrap.stopped and a terminal real
scan in this receipt; the factory never writes one itself.
"""
import argparse
import hashlib
import json
import os
import secrets
import shutil
import signal
import sqlite3
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
import zipfile
from pathlib import Path

MAX_MEDIA_FILES = 1000
MAX_IDENTITIES = 100
STOP_BYTES = 7 * 1024 ** 3
HARD_BYTES = 8 * 1024 ** 3
CAMPAIGN_HOST = '100.120.50.24'
LOOPBACK = '127.0.0.1'


def fail(message, code=2):
    print(message, file=sys.stderr)
    raise SystemExit(code)


def refuse_ci(parser):
    if 'CI' in os.environ or 'GITHUB_ACTIONS' in os.environ:
        parser.error('Fixture preparation and live provisioning are prohibited in CI')


def private_path(path, mode=0o600, what='file'):
    path = Path(path)
    try:
        stat = path.lstat()
    except FileNotFoundError:
        fail(f'{what} does not exist: {path}')
    if path.is_symlink() or stat.st_uid != os.getuid():
        fail(f'{what} must be owned by this user and not a symlink: {path}')
    if mode is not None and (stat.st_mode & 0o777) != mode:
        fail(f'{what} must be mode {oct(mode)}: {path}')
    return path


def owned_directory(path, create=False):
    path = Path(path)
    if not path.exists():
        if not create:
            fail(f'directory does not exist: {path}')
        path.mkdir(mode=0o700)
        os.chmod(path, 0o700)
    stat = path.lstat()
    if path.is_symlink() or not path.is_dir() or stat.st_uid != os.getuid() or (stat.st_mode & 0o777) != 0o700:
        fail(f'directory must be an owned 0700 directory: {path}')
    return path


def read_private_json(path, maximum=4 * 1024 * 1024):
    path = private_path(path)
    if path.stat().st_size > maximum:
        fail(f'private input is larger than {maximum} bytes: {path}')
    try:
        return json.loads(path.read_text())
    except ValueError:
        fail(f'private input is not valid JSON: {path}')


def write_private_json(path, document, mode=0o600):
    path = Path(path)
    temporary = path.with_name(path.name + '.' + secrets.token_hex(6) + '.tmp')
    fd = os.open(temporary, os.O_CREAT | os.O_EXCL | os.O_WRONLY | os.O_NOFOLLOW, mode)
    try:
        with os.fdopen(fd, 'w') as output:
            json.dump(document, output, indent=1, sort_keys=True)
            output.write('\n')
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, path)
    finally:
        if temporary.exists():
            temporary.unlink()
    directory = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(directory)
    finally:
        os.close(directory)


def receipt_of(document):
    payload = json.dumps({key: value for key, value in document.items() if key != 'receipt'},
                         sort_keys=True, separators=(',', ':')).encode()
    return 'appdata-' + hashlib.sha256(payload).hexdigest()


def probe_duration(path):
    try:
        result = subprocess.run(['ffprobe', '-v', 'error', '-show_entries', 'format=duration',
                                 '-of', 'default=nw=1:nk=1', str(path)],
                                capture_output=True, text=True, timeout=120)
    except (OSError, subprocess.SubprocessError):
        return None
    if result.returncode != 0:
        return None
    try:
        value = float(result.stdout.strip())
    except ValueError:
        return None
    return value if value > 0 else None


def probe_video(path):
    try:
        result = subprocess.run(['ffprobe', '-v', 'error', '-select_streams', 'v:0',
                                 '-show_entries', 'stream=codec_name,width,height',
                                 '-of', 'json', str(path)], capture_output=True, text=True, timeout=120)
        stream = (json.loads(result.stdout).get('streams') or [{}])[0]
    except (OSError, subprocess.SubprocessError, ValueError):
        stream = {}
    return {'video': stream.get('codec_name') or 'none', 'width': int(stream.get('width') or 0),
            'height': int(stream.get('height') or 0)}


def occupied_bytes(path):
    """Physical blocks in a tree and inodes referenced, hard links counted once."""
    blocks, seen = 0, set()
    for root, directories, files in os.walk(path):
        for name in directories:
            stat = os.lstat(os.path.join(root, name))
            seen.add((stat.st_dev, stat.st_ino))
        for name in files:
            stat = os.lstat(os.path.join(root, name))
            key = (stat.st_dev, stat.st_ino)
            if key not in seen:
                seen.add(key)
                blocks += stat.st_blocks * 512
    return blocks, len(seen)


# ── media ───────────────────────────────────────────────────────────────────────────────
def write_epub(path, title):
    """A real, valid EPUB 3: zip with an uncompressed mimetype first, then the package."""
    with zipfile.ZipFile(path, 'w') as archive:
        archive.writestr(zipfile.ZipInfo('mimetype'), 'application/epub+zip', compress_type=zipfile.ZIP_STORED)
        archive.writestr('META-INF/container.xml',
                         '<?xml version="1.0"?><container version="1.0" '
                         'xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles>'
                         '<rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>'
                         '</rootfiles></container>', compress_type=zipfile.ZIP_DEFLATED)
        archive.writestr('OEBPS/content.opf',
                         '<?xml version="1.0" encoding="utf-8"?><package version="3.0" unique-identifier="id" '
                         'xmlns="http://www.idpf.org/2007/opf"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/">'
                         f'<dc:identifier id="id">urn:uuid:{secrets.token_hex(16)}</dc:identifier>'
                         f'<dc:title>{title}</dc:title><dc:language>en</dc:language></metadata><manifest>'
                         '<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>'
                         '<item id="c1" href="chapter1.xhtml" media-type="application/xhtml+xml"/></manifest>'
                         '<spine><itemref idref="c1"/></spine></package>', compress_type=zipfile.ZIP_DEFLATED)
        archive.writestr('OEBPS/nav.xhtml',
                         '<?xml version="1.0" encoding="utf-8"?><html xmlns="http://www.w3.org/1999/xhtml" '
                         'xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Contents</title></head><body>'
                         '<nav epub:type="toc"><ol><li><a href="chapter1.xhtml">Chapter 1</a></li></ol></nav>'
                         '</body></html>', compress_type=zipfile.ZIP_DEFLATED)
        archive.writestr('OEBPS/chapter1.xhtml',
                         '<?xml version="1.0" encoding="utf-8"?><html xmlns="http://www.w3.org/1999/xhtml">'
                         '<head><title>Chapter 1</title></head><body><h1>Chapter 1</h1>'
                         '<p>' + ('Reading fixture paragraph. ' * 40) + '</p></body></html>',
                         compress_type=zipfile.ZIP_DEFLATED)


def command_media(args, parser):
    refuse_ci(parser)
    root = Path(args.root)
    if root.exists() or args.execute:
        root = owned_directory(root, create=args.execute)
    sources = []
    for raw in args.source:
        path = private_path(raw, mode=None, what='source media')
        duration = probe_duration(path)
        if duration is None:
            fail(f'ffprobe could not measure source media: {path}')
        if duration < args.window_seconds + 60:
            fail(f'source media is shorter than the window plus a minute: {path}')
        sources.append({'path': path, 'duration': duration, **probe_video(path)})
    if not sources:
        fail('at least one --source real media file is required')
    count = args.scalable_count or args.count
    scalable = bool(args.scalable_count)
    if scalable:
        fail('superseded large fixture; use ingest/run.py for the 300k real-scan campaign')
    elif not 1 <= count <= MAX_MEDIA_FILES:
        fail(f'count must be 1..{MAX_MEDIA_FILES}; use ingest/run.py for the 300k fixture')
    # Each extra name costs directory metadata even when its bytes are shared by a hard link.
    NAME_OVERHEAD_BYTES = 4096
    # Plan first: nothing is created and no link is made until --execute.
    unique_bytes = sum(source['path'].stat().st_blocks * 512 for source in sources)
    if args.distinct_copies:
        unique_bytes = max(unique_bytes, count * max(source['path'].stat().st_blocks * 512 for source in sources))
    media_blocks, _ = occupied_bytes(root) if root.exists() else (0, 0)
    appdata_blocks = 0
    if args.appdata and Path(args.appdata).exists():
        appdata_blocks, _ = occupied_bytes(Path(args.appdata))
    projected = max(media_blocks, unique_bytes) + appdata_blocks + (count * NAME_OVERHEAD_BYTES if scalable else 0)
    plan = {'command': 'media', 'root': str(root), 'files': count, 'hardlinked': True,
            'sourceFiles': [str(source['path']) for source in sources],
            'projectedBytes': projected, 'stopBudget': STOP_BYTES, 'hardBudget': HARD_BYTES,
            'windowSeconds': args.window_seconds,
            'minDurationSeconds': int(min(source['duration'] for source in sources))}
    refused = projected > STOP_BYTES
    if refused or projected > HARD_BYTES:
        plan['state'] = 'REFUSED'
        plan['projectedBytes'] = projected
        plan['hardBudget'] = HARD_BYTES
        # The exact failure is retained as evidence; nothing is created and no 5M claim is made.
        if args.record:
            write_private_json(args.record, {'kind': 'portico-media-refusal', 'version': 1,
                                             'generatedAt': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()), **plan})
        print(json.dumps(plan))
        fail(f'projected physical accounting {projected} exceeds the {"8 GiB hard" if projected > HARD_BYTES else "7 GiB stop"} budget; '
             f'nothing was created and no scalable fixture is claimed', 3)
    if count > len(sources) * args.max_links:
        # A hard link needs one inode, one source inode and a bounded per-inode link count.
        fail(f'{count} names exceed the {args.max_links}-link budget of {len(sources)} sources; '
             f'add sources or raise --max-links', 3)
    if not args.execute:
        print(json.dumps({'state': 'PLAN', **plan}))
        return
    library = root / 'movies'
    library.mkdir(mode=0o700, exist_ok=True)
    files = []
    for index in range(count):
        source = sources[index % len(sources)]
        # One folder per title: the product reads a movie from its own folder, so grouped files in
        # one folder would (correctly) collapse into a single film and could not be counted as 100.
        directory = library / f'movie-{index:04d}'
        directory.mkdir(mode=0o700, exist_ok=True)
        target = directory / f'movie-{index:04d}{source["path"].suffix}'
        if target.exists():
            fail(f'refusing to replace an existing fixture file: {target}')
        if args.distinct_copies:
            # Distinct bytes per title (same streams, unique metadata) so the scanner's content
            # dedupe yields distinct films. A stream copy, never a re-encode.
            ffmpeg = os.environ.get('PORTICO_FFMPEG') or shutil.which('ffmpeg') or 'ffmpeg'
            result = subprocess.run([ffmpeg, '-v', 'error', '-y', '-i', str(source['path']), '-c', 'copy',
                                     '-metadata', f'title=Scale Movie {index:04d}', str(target)],
                                    capture_output=True, timeout=120)
            if result.returncode != 0 or not target.exists():
                fail(f'could not write a distinct copy for {target}')
        else:
            try:
                os.link(source['path'], target)
            except OSError as error:
                if error.errno == 18:
                    fail('the media root must be on the same filesystem as the source media; hard links cannot cross devices')
                raise
        files.append({'path': str(target), 'sizeBytes': target.stat().st_size,
                      'modifiedNs': target.stat().st_mtime_ns,
                      'durationSeconds': source['duration'], 'container': source['path'].suffix.lstrip('.').lower(),
                      'videoCodec': source['video'], 'audioCodec': 'aac',
                      'width': source['width'] or 1, 'height': source['height'] or 1})
    books = root / 'books'
    books.mkdir(mode=0o700, exist_ok=True)
    epub = books / 'reading-fixture.epub'
    if not epub.exists():
        write_epub(epub, 'Reading Fixture')
    media_blocks, media_inodes = occupied_bytes(root)
    appdata_blocks = 0
    if args.appdata and Path(args.appdata).exists():
        appdata_blocks, _ = occupied_bytes(Path(args.appdata))
    accounted = media_blocks + appdata_blocks
    if accounted > STOP_BYTES:
        print(json.dumps({**plan, 'state': 'REFUSED', 'accountedBytes': accounted}))
        fail(f'physical accounting {accounted} exceeds the 7 GiB stop budget', 3)
    facts = {'version': 1, 'kind': 'portico-media', 'root': str(root), 'files': files,
             'epub': str(epub), 'windowSeconds': args.window_seconds,
             'physicalBytes': accounted, 'uniqueInodes': media_inodes}
    write_private_json(args.out, facts)
    print(json.dumps({**plan, 'state': 'PREPARED', 'mediaBytes': media_blocks, 'appdataBytes': appdata_blocks,
                      'accountedBytes': accounted, 'uniqueInodes': media_inodes, 'out': str(args.out)}))


# ── HTTP client ─────────────────────────────────────────────────────────────────────────
class Refusal(Exception):
    def __init__(self, status, method, path, detail, code=None):
        super().__init__(f'{method} {path} refused with {status}')
        self.status = status
        self.code = code
        self.detail = detail


class Client:
    def __init__(self, base, installation, origin=None):
        self.base = base.rstrip('/')
        self.installation = installation
        # Browser setup is origin-bound; API calls keep the native shape (no Origin).
        self.origin = origin
        self.token = ''

    def call(self, method, path, body=None, ok=(200,), timeout=60):
        data = None if body is None else json.dumps(body).encode()
        request = urllib.request.Request(self.base + path, data=data, method=method)
        request.add_header('Accept', 'application/json')
        if self.origin:
            request.add_header('Origin', self.origin)
        if self.installation:
            request.add_header('X-Portico-Installation-Id', self.installation)
        if data is not None:
            request.add_header('Content-Type', 'application/json')
        if self.token:
            request.add_header('Authorization', 'Bearer ' + self.token)
        try:
            with urllib.request.urlopen(request, timeout=timeout) as response:
                payload = response.read()
                if response.status not in ok:
                    raise Refusal(response.status, method, path, '', None)
                return json.loads(payload) if payload else None
        except urllib.error.HTTPError as error:
            detail = error.read(4096).decode('utf-8', 'replace')
            code = None
            try:
                code = (json.loads(detail).get('error') or {}).get('code')
            except ValueError:
                pass
            raise Refusal(error.code, method, path, detail, code) from None

    def read(self, path, timeout=60):
        return self.call('GET', path, timeout=timeout)


def retrying(action, attempts=8, base=1.5):
    last = None
    for attempt in range(attempts):
        try:
            return action()
        except Refusal as refusal:
            last = refusal
            if refusal.status not in (409, 425, 429, 503, 504) or attempt == attempts - 1:
                raise
            time.sleep(min(20, base * (attempt + 1)))
    raise last


def items(document):
    if isinstance(document, list):
        return document
    if isinstance(document, dict):
        for key in ('items', 'entries', 'libraries', 'members', 'profiles', 'entries'):
            value = document.get(key)
            if isinstance(value, list):
                return value
    return []


def sign_in(base, username, password, installation):
    client = Client(base, installation)
    signed = retrying(lambda: client.call('POST', '/v1/direct/sign-in',
                                          {'username': username, 'password': password}, ok=(200,)))
    session = signed.get('session', signed)
    client.token = session['accessToken']
    return client, session, signed.get('profiles') or []


def select_profile(client, profile_id):
    selected = retrying(lambda: client.call(
        'POST', '/v1/direct/profiles/' + urllib.parse.quote(profile_id, safe='') + '/select',
        {'installationId': client.installation, 'trust': False}, ok=(200,)))
    session = selected['session']
    client.token = session['accessToken']
    return session


def current_admission(client, session, role):
    """The session used must be the one the server admits right now: a Local owner family
    retired by an owner move answers 401 here and is refused, never carried into a manifest."""
    try:
        me = client.read('/v1/me')
    except Refusal as refusal:
        if refusal.status in (401, 403):
            fail('the owner sign-in is not the current owner admission (retired family after an owner move?); '
                 're-admit the current Hosted owner and re-run')
        raise
    viewer = (me or {}).get('viewer') or {}
    if viewer.get('role') != role:
        fail(f'the server reports role {viewer.get("role")!r}, expected {role!r}')
    if viewer.get('accountId') != session['viewer']['accountId'] or viewer.get('profileId') != session['viewer']['profileId']:
        fail('the admitted viewer does not match the sign-in envelope')
    return viewer


def envelope_identity(session, role):
    required = ('accessToken', 'refreshToken', 'deviceId', 'installationId', 'sessionFamilyId',
                'tokenGeneration', 'authorizationHorizon', 'expiresAt', 'viewer', 'serverIdentity')
    missing = [key for key in required if not session.get(key)]
    if missing:
        fail('sign-in envelope is missing ' + ','.join(missing) + '; rotation would be unprovable')
    viewer = session['viewer']
    return {'accessToken': session['accessToken'], 'refreshToken': session['refreshToken'],
            'expiresAt': session['expiresAt'], 'sessionFamilyId': session['sessionFamilyId'],
            'tokenGeneration': str(session['tokenGeneration']), 'authorizationHorizon': session['authorizationHorizon'],
            'authority': viewer['authority'], 'accountId': viewer['accountId'], 'profileId': viewer['profileId'],
            'deviceId': session['deviceId'], 'installationId': session['installationId'], 'role': role,
            'serverIdentity': session['serverIdentity']}


# ── bootstrap ───────────────────────────────────────────────────────────────────────────
def wait_ready(base, seconds):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen(base + '/v1/system', timeout=5) as response:
                if response.status == 200:
                    return json.loads(response.read())
        except (urllib.error.URLError, OSError, ValueError):
            pass
        time.sleep(0.5)
    return None


def scan_sources(client, library_id):
    document = client.read('/v1/libraries/' + urllib.parse.quote(library_id, safe='') + '/inventory-status')
    return ((document or {}).get('inventory') or {}).get('sources') or []


def wait_scan(client, library_id, timeout):
    deadline = time.monotonic() + timeout
    last = []
    while time.monotonic() < deadline:
        last = scan_sources(client, library_id)
        if last and all(source.get('status') in ('complete', 'complete_with_warnings') for source in last):
            return last
        if any(source.get('status') in ('failed', 'cancelled') for source in last):
            return last
        time.sleep(1)
    return last


def library_items(client, library_id, pivot='movies', maximum=1000):
    found, cursor = [], ''
    while len(found) < maximum:
        body = {'pivot': pivot, 'limit': 100}
        if cursor:
            body['cursor'] = cursor
        page = retrying(lambda: client.call('POST', '/v1/libraries/' + urllib.parse.quote(library_id, safe='') + '/browse',
                                            body, ok=(200,)))
        found.extend(page.get('entries') or [])
        cursor = (page.get('pageInfo') or {}).get('nextCursor') or ''
        if not cursor:
            break
    return found[:maximum]


def schema_readback(database):
    """The stopped database's own inventory. Names are discovered, never assumed."""
    connection = sqlite3.connect('file:' + urllib.parse.quote(str(database)) + '?mode=ro', uri=True)
    try:
        tables = [row[0] for row in connection.execute(
            "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")]
        user_version = connection.execute('PRAGMA user_version').fetchone()[0]
        counts = {}
        for name in tables:
            if any(token in name for token in ('asset', 'source', 'root', 'library', 'entit')):
                try:
                    counts[name] = connection.execute(f'SELECT COUNT(*) FROM "{name}"').fetchone()[0]
                except sqlite3.DatabaseError:
                    counts[name] = None
        return {'tables': len(tables), 'tableNames': tables[:400], 'userVersion': user_version,
                'relevantCounts': counts}
    finally:
        connection.close()


def command_bootstrap(args, parser):
    refuse_ci(parser)
    binary = private_path(args.server_binary, mode=None, what='server binary')
    if not os.access(binary, os.X_OK):
        fail(f'server binary is not executable: {binary}')
    appdata = Path(args.appdata)
    if appdata.exists() and any(appdata.iterdir()):
        fail(f'appdata must be absent or empty; never reuse or overwrite an appdata: {appdata}')
    if appdata.exists() or args.execute:
        owned_directory(appdata, create=args.execute)
    facts = read_private_json(args.media_facts)
    files = facts.get('files') or []
    if not files:
        fail('media facts contain no files')
    if len(files) > MAX_MEDIA_FILES:
        fail(f'at most {MAX_MEDIA_FILES} media files are bootstrapped in one tiny fixture')
    media_root = Path(facts['root'])
    if not media_root.is_dir():
        fail(f'media root is missing: {media_root}')
    port = args.port
    base = f'http://{LOOPBACK}:{port}'
    plan = {'command': 'bootstrap', 'base': base, 'appdata': str(appdata), 'mediaFiles': len(files),
            'windowSeconds': args.window_seconds, 'scanTimeoutSeconds': args.scan_timeout}
    if not args.execute:
        print(json.dumps({'state': 'PLAN', **plan}))
        return
    log_path = appdata / 'bootstrap.log'
    log = open(log_path, 'wb')
    os.chmod(log_path, 0o600)
    environment = {'PATH': os.environ.get('PATH', '/usr/bin:/bin'), 'HOME': str(appdata),
                   'PORTICO_DATA_DIR': str(appdata), 'PORTICO_BIND': f'{LOOPBACK}:{port}',
                   'PORTICO_SETUP_HOSTS': f'localhost:{port},{LOOPBACK}:{port}',
                   'GOMAXPROCS': '2'}
    runtime_tmp = Path(os.environ.get('TMPDIR') or str(appdata.parent / (appdata.name + '-runtime-tmp')))
    owned_directory(runtime_tmp, create=True)
    environment['TMPDIR'] = str(runtime_tmp)
    for key in ('PORTICO_FFMPEG', 'PORTICO_FFPROBE', 'PORTICO_RCLONE'):
        if key in os.environ: environment[key] = os.environ[key]
    process = subprocess.Popen([str(binary)], stdout=log, stderr=log, stdin=subprocess.DEVNULL,
                               env=environment, cwd=str(appdata), start_new_session=True)
    started = time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())
    stop_reason = None
    receipt = {'version': 1, 'kind': 'portico-appdata', 'generatedAt': started,
               'bootstrap': {'base': base, 'started': True, 'stopped': False},
               'media': {'files': len(files), 'minDurationSeconds': int(min(f['durationSeconds'] for f in files)),
                         'durationSource': 'probe', 'root': str(media_root)},
               'scan': {}, 'playable': {}, 'schema': {}}
    try:
        ready = wait_ready(base, args.ready_timeout)
        if ready is None:
            fail('the server never answered /v1/system; see bootstrap.log in the appdata directory', 4)
        receipt['bootstrap']['readySeconds'] = round(time.monotonic(), 3)
        owner = {'username': args.owner_username, 'password': secrets.token_urlsafe(24)}
        write_private_json(appdata / 'owner-credentials.json', owner)
        installation = secrets.token_urlsafe(24)
        setup = Client(base, installation, origin=base)
        try:
            browser = setup.call('POST', '/v1/setup/browser', {}, ok=(200,))
            setup.call('POST', '/v1/setup', {'setupToken': browser['setupToken'],
                                            'requestId': str(uuid.uuid4()), 'name': 'P2-65 isolated fixture',
                                            'authMode': 'local', 'interactive': True,
                                            'username': owner['username'], 'password': owner['password']},
                       ok=(201,))
        except Refusal as refusal:
            fail(f'ordinary setup refused with {refusal.status}; the appdata may already be claimed', 4)
        owner_client, owner_session, profiles = sign_in(base, owner['username'], owner['password'], installation)
        primary = next((entry for entry in profiles if entry.get('primary')), profiles[0])
        owner_session = select_profile(owner_client, primary['id'])
        owner_viewer = current_admission(owner_client, owner_session, 'owner')
        library = retrying(lambda: owner_client.call('POST', '/v1/libraries', {
            'name': 'Scale Movies', 'kind': 'movie', 'path': str(media_root / 'movies'),
            'metadataAgent': 'local'}, ok=(200, 201)))
        library_id = (library.get('library', library)).get('id')
        receipt['library'] = {'id': library_id, 'kind': 'movie', 'path': str(media_root / 'movies')}
        books = retrying(lambda: owner_client.call('POST', '/v1/libraries', {
            'name': 'Scale Books', 'kind': 'book', 'path': str(media_root / 'books'),
            'metadataAgent': 'local'}, ok=(200, 201)))
        books_id = (books.get('library', books)).get('id')
        receipt['readingLibrary'] = {'id': books_id, 'kind': 'book', 'path': str(media_root / 'books')}
        for target_id, label in ((library_id, 'movies'), (books_id, 'books')):
            sources = scan_sources(owner_client, target_id)
            if not sources or not any(int(source.get('discovered') or 0) > 0 for source in sources):
                owner_client.call('POST', '/v1/libraries/' + urllib.parse.quote(target_id, safe='') + '/scans',
                                  {}, ok=(200, 201, 202))
            scanned = wait_scan(owner_client, target_id, args.scan_timeout)
            receipt.setdefault('scans', []).append({
                'library': label, 'terminal': bool(scanned) and all(s.get('status') in ('complete', 'complete_with_warnings') for s in scanned),
                'failed': any(s.get('status') in ('failed', 'cancelled') for s in scanned),
                'sources': [{'status': s.get('status'), 'discovered': s.get('discovered'), 'analyzed': s.get('analyzed'),
                             'warnings': s.get('warnings')} for s in scanned]})
        receipt['scan'] = next((entry for entry in receipt['scans'] if entry['library'] == 'movies'), {})
        if not receipt['scan'].get('terminal') or receipt['scan'].get('failed'):
            stop_reason = 'scan_not_terminal'
        entries = library_items(owner_client, library_id)
        distinct = len({entry.get('id') for entry in entries if entry.get('id')})
        receipt['playable'] = {'source': 'scanned-api', 'items': distinct,
                               'itemIds': [entry.get('id') for entry in entries if entry.get('id')][:MAX_MEDIA_FILES],
                               'minDurationSeconds': receipt['media']['minDurationSeconds'],
                               'durationSource': 'probe',
                               # The catalogue is the evidence, not the fixture's intent: fewer
                               # titles than prepared names is reported, never called 100 items.
                               'preparedNames': len(files), 'distinctTitles': distinct,
                               'distinctComplete': distinct >= len(files)}
        if distinct < len(files):
            stop_reason = stop_reason or 'prepared_names_not_distinct_titles'
        # The workload template comes from what the server actually offers, not from this script.
        # The simulated consumer runs at the item's real average bitrate: the smallest prepared
        # file over its probed duration. A guessed higher rate would claim bytes the file cannot hold.
        smallest = min(int(file['sizeBytes']) for file in files)
        bitrate = max(1024, round(smallest / max(1.0, receipt['media']['minDurationSeconds'])))
        workload = {'libraryId': library_id, 'pivot': 'movies',
                    'itemIds': receipt['playable']['itemIds'][:3] or [],
                    'search': 'item', 'bytesPerSecond': bitrate,
                    'reading': {'libraryId': books_id, 'pivot': 'books', 'facet': ''}}
        try:
            capabilities = owner_client.read('/v1/libraries/' + urllib.parse.quote(books_id, safe='') +
                                             '/browse-capabilities?pivot=books')
        except Refusal as refusal:
            capabilities = None
            receipt.setdefault('warnings', []).append({'step': 'reading-capabilities', 'status': refusal.status})
        candidates = []
        for pivot in (capabilities or {}).get('pivots') or []:
            source = (pivot or {}).get('facetSource') or {}
            if source.get('field'):
                candidates.append(str(source['field']))
        # Reading facets are the book fields the prepared-facet protocol serves.
        candidates.extend(['author', 'series'])
        for field in (capabilities or {}).get('fields') or []:
            if isinstance(field, dict) and field.get('id'):
                candidates.append(str(field['id']))
        for field in list(dict.fromkeys(candidates))[:10]:
            try:
                # 202 accepted is the prepared-reading protocol: the read arm polls it.
                owner_client.read('/v1/libraries/' + urllib.parse.quote(books_id, safe='') + '/facets?' +
                                  urllib.parse.urlencode({'field': field, 'pivot': 'books', 'limit': 20}))
                workload['reading']['facet'] = field
                break
            except Refusal as refusal:
                if refusal.status in (200, 202):
                    workload['reading']['facet'] = field
                    break
                continue
        receipt['workload'] = workload
        if workload['itemIds']:
            write_private_json(appdata / 'workload-template.json', workload)
        storage = owner_client.read('/v1/admin/storage/status')
        receipt['storage'] = {'databaseUnderAppdata': str(storage.get('databasePath', '')).startswith(str(appdata)),
                              'reclaimableBytes': storage.get('reclaimableBytes'),
                              'databaseBytes': Path(storage.get('databasePath', '')).stat().st_size
                              if storage.get('databasePath') and Path(storage['databasePath']).exists() else None}
        receipt['databasePath'] = storage.get('databasePath')
        if not receipt['storage']['databaseUnderAppdata']:
            stop_reason = stop_reason or 'database_outside_appdata'
        write_private_json(appdata / 'owner-identity.json',
                           {**envelope_identity({**owner_session, 'viewer': owner_viewer}, 'owner'),
                            'capabilities': read_private_json(args.capabilities) if args.capabilities else None})
        receipt['serverId'] = (owner_client.read('/v1/system') or {}).get('id')
    except BaseException:
        stop_reason = stop_reason or 'bootstrap_interrupted_or_failed'
        raise
    finally:
        receipt['bootstrap']['stopped'] = False
        if process.poll() is None:
            os.killpg(process.pid, signal.SIGTERM)
            try:
                process.wait(timeout=args.stop_timeout)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait(timeout=10)
                stop_reason = stop_reason or 'server_required_sigkill'
        receipt['bootstrap']['stopped'] = process.poll() is not None
        receipt['bootstrap']['exitCode'] = process.returncode
        log.close()
        if receipt.get('databasePath') and not stop_reason:
            receipt['schema'] = schema_readback(receipt['databasePath'])
        receipt['stoppedAt'] = time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())
        receipt['blocker'] = stop_reason
        receipt['receipt'] = receipt_of(receipt)
        write_private_json(appdata / 'prep-receipt.json', receipt)
    print(json.dumps({key: receipt.get(key) for key in ('receipt', 'serverId', 'library', 'scan', 'playable',
                                                        'storage', 'schema', 'blocker')}, sort_keys=True))
    if stop_reason:
        fail(f'bootstrap incomplete: {stop_reason} (receipt retains the exact failure)', 5)


# ── identities ──────────────────────────────────────────────────────────────────────────
def command_identities(args, parser):
    refuse_ci(parser)
    target = urllib.parse.urlparse(args.target)
    if target.scheme != 'http' or target.hostname not in {LOOPBACK, CAMPAIGN_HOST}:
        fail('target must be http://127.0.0.1:<port> (rehearsal) or the allowlisted campaign host')
    if not 1 <= args.members <= MAX_IDENTITIES:
        fail(f'members must be between 1 and {MAX_IDENTITIES}')
    owner = read_private_json(args.owner_credentials)
    if not owner.get('username') or not owner.get('password'):
        fail('owner credentials need username and password')
    capabilities = read_private_json(args.capabilities)
    plan = {'command': 'identities', 'target': args.target, 'members': args.members,
            'identities': args.members + 2, 'pacingSeconds': args.pacing_seconds}
    if not args.execute:
        print(json.dumps({'state': 'PLAN', **plan}))
        return
    base = args.target.rstrip('/')
    owner_client, owner_session, profiles = sign_in(base, owner['username'], owner['password'], secrets.token_urlsafe(24))
    primary = next((entry for entry in profiles if entry.get('primary')), profiles[0])
    owner_session = select_profile(owner_client, primary['id'])
    owner_viewer = current_admission(owner_client, owner_session, 'owner')
    server_id = (owner_client.read('/v1/system') or {}).get('id')
    libraries = items(owner_client.read('/v1/libraries'))
    library_ids = [entry['id'] for entry in libraries]
    if not library_ids:
        fail('the server returned no libraries; bootstrap the fixture library first')
    existing = {entry.get('username') for entry in items(owner_client.read('/v1/direct/members'))}
    recorded = [envelope_identity({**owner_session, 'viewer': owner_viewer}, 'owner')]
    created = 0
    cohort = args.members          # measured users
    total_members = cohort + 1     # + the quiet member: 100 users + quiet + owner = 102
    # Passwords are written down before the create POST: a partial POST (created account, lost
    # answer) must never leave an account nobody can sign in as.
    custody = {'version': 1, 'kind': 'portico-member-custody', 'target': args.target, 'members': {}}
    custody_path = args.custody or (Path(args.out).with_name('member-custody.json'))
    for index in range(total_members):
        username = f'{args.username_prefix}-{index + 1:03d}'
        if username in existing:
            fail(f'{username} already exists without its saved credentials; refusing to reuse an unknown account')
        password = secrets.token_urlsafe(24)
        custody['members'][username] = {'username': username, 'password': password, 'created': False}
        write_private_json(custody_path, custody)
        retrying(lambda: owner_client.call('POST', '/v1/direct/members', {
            'username': username, 'password': password, 'name': f'Scale User {index + 1:03d}',
            'allowedLibraries': library_ids}, ok=(201,)))
        custody['members'][username] = {'username': username, 'password': password, 'created': True}
        write_private_json(custody_path, custody)
        created += 1
        time.sleep(args.pacing_seconds)
        member_client, _member_session, member_profiles = sign_in(base, username, password, secrets.token_urlsafe(24))
        member_primary = next((entry for entry in member_profiles if entry.get('primary')), member_profiles[0])
        selected = select_profile(member_client, member_primary['id'])
        member_viewer = current_admission(member_client, selected, 'member')
        recorded.append(envelope_identity({**selected, 'viewer': member_viewer}, 'member'))
        time.sleep(args.pacing_seconds)
    for key in ('accessToken', 'refreshToken', 'accountId', 'profileId', 'deviceId', 'installationId', 'sessionFamilyId'):
        if len({entry[key] for entry in recorded}) != len(recorded):
            fail(f'provisioned identities are not distinct on {key}')
    document = {'version': 1, 'kind': 'portico-identities',
                'generatedAt': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()),
                'target': args.target, 'serverId': server_id, 'capabilities': capabilities,
                'libraries': library_ids, 'owner': recorded[0],
                'members': recorded[1:1 + cohort], 'quietMember': recorded[1 + cohort],
                'cohort': cohort, 'totalIdentities': cohort + 2, 'custody': str(custody_path),
                'createdMembers': created}
    write_private_json(args.out, document)
    print(json.dumps({'identities': len(recorded), 'cohort': cohort, 'quiet': 1, 'owner': 1,
                      'totalIdentities': cohort + 2, 'createdMembers': created, 'serverId': server_id,
                      'libraries': len(library_ids), 'custody': str(custody_path), 'out': str(args.out)}))


# ── manifest ────────────────────────────────────────────────────────────────────────────
def command_manifest(args, parser):
    refuse_ci(parser)
    identities = read_private_json(args.identities)
    receipt = read_private_json(args.prep_receipt)
    workload = read_private_json(args.workload)
    if identities.get('kind') != 'portico-identities' or not identities.get('serverId'):
        fail('identities file is not a portico-identities document')
    if receipt.get('kind') != 'portico-appdata' or not receipt.get('receipt'):
        fail('preparation receipt is not a portico-appdata receipt')
    bootstrap = receipt.get('bootstrap') or {}
    if not bootstrap.get('stopped'):
        fail('the preparation receipt does not prove a stopped server; start/stop readback is required')
    if not (receipt.get('scan') or {}).get('terminal'):
        fail('the preparation receipt has no terminal real scan')
    playable = receipt.get('playable') or {}
    if not playable.get('items') or playable.get('durationSource') != 'probe':
        fail('the preparation receipt has no probed scanned playable set')
    users = list(identities.get('members') or [])
    if not users:
        fail('at least one member identity is required')
    for key in ('libraryId', 'pivot', 'itemIds', 'search'):
        if not workload.get(key):
            fail(f'workload needs {key}')
    if not isinstance(workload['itemIds'], list) or not 1 <= len(workload['itemIds']) <= 20:
        fail('workload itemIds must be 1..20 known playable item ids')
    if not workload.get('reading') or not all(workload['reading'].get(k) for k in ('libraryId', 'pivot', 'facet')):
        fail('workload needs reading {libraryId,pivot,facet}')

    def user(identity):
        return {**identity, 'libraryId': workload['libraryId'], 'pivot': workload['pivot'],
                'itemIds': workload['itemIds'], 'search': workload['search'],
                'bytesPerSecond': int(workload.get('bytesPerSecond', 3_000_000)),
                'directVerifiedAt': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()),
                'reading': workload['reading'], 'capabilities': identities['capabilities']}
    manifest = {'version': 1, 'target': identities['target'], 'serverId': identities['serverId'],
                'conversionMode': 'never', 'stateAdapterReceipt': receipt['receipt'],
                'owner': {**identities['owner'], 'libraryId': workload['libraryId'],
                          'capabilities': identities['capabilities']},
                'quietMember': {**identities['quietMember'], 'capabilities': identities['capabilities']},
                'users': [user(entry) for entry in users]}
    every = [('owner', manifest['owner']), ('quietMember', manifest['quietMember'])] + \
            [(f'users[{index}]', entry) for index, entry in enumerate(manifest['users'])]
    for label, identity in every:
        for key in ('accessToken', 'refreshToken', 'sessionFamilyId', 'tokenGeneration', 'authorizationHorizon',
                    'expiresAt', 'installationId', 'deviceId', 'accountId', 'profileId', 'authority', 'role', 'capabilities'):
            if not identity.get(key):
                fail(f'{label} is missing {key}; rotation and capability publication must be provable')
        server_identity = identity.get('serverIdentity') or {}
        if len(server_identity.get('publicKey', '')) != 43 or len(server_identity.get('fingerprint', '')) != 43:
            fail(f'{label} needs the exact captured serverIdentity publicKey/fingerprint')
    for key in ('accessToken', 'refreshToken', 'accountId', 'profileId', 'deviceId', 'installationId', 'sessionFamilyId'):
        if len({identity[key] for _, identity in every}) != len(every):
            fail(f'manifest identities are not distinct on {key}')
    if manifest['owner']['role'] != 'owner' or manifest['quietMember']['role'] != 'member':
        fail('the owner must be the owner and the quiet identity a member')
    plan = {'command': 'manifest', 'users': len(manifest['users']), 'target': manifest['target'],
            'stateAdapterReceipt': manifest['stateAdapterReceipt']}
    if not args.execute:
        print(json.dumps({'state': 'PLAN', **plan}))
        return
    write_private_json(args.out, manifest)
    print(json.dumps({**plan, 'out': str(args.out)}))


# ── proposal ────────────────────────────────────────────────────────────────────────────
PROPOSAL = {
    'kind': 'portico-real-scan-ingest-plan', 'version': 2, 'status': '300k test-round-only; not executed',
    'supersedes': '1M/5M and proportional-reference builders; do not execute those designs',
    'command': 'python3 scripts/batch-round/ingest/run.py --help',
    'files': 300000, 'mix': {'movies': 90000, 'episodes': 150000, 'music': 60000},
    'path': 'distinct valid media -> real scans -> exact leaf/availability counts and offered facet reconciliation',
    'measurements': ['per50k ingest rate and all-writer time/item', 'before/during/after per-operation p50/p99',
                     'resident browse/search/Home at50k/150k/300k', 'memory, total time, count/facet/availability convergence'],
    'stopBytes': STOP_BYTES, 'kernelHardBytes': HARD_BYTES,
}


def command_proposal(args, parser):
    refuse_ci(parser)
    print(json.dumps(PROPOSAL, indent=1))
    if args.out:
        write_private_json(args.out, PROPOSAL)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    commands = parser.add_subparsers(dest='command', required=True)

    media = commands.add_parser('media', help='generate hard-linked real media + EPUB under a bounded budget')
    media.add_argument('--root', type=Path, required=True)
    media.add_argument('--source', type=Path, action='append', required=True)
    media.add_argument('--count', type=int, default=100)
    media.add_argument('--scalable-count', type=int)
    media.add_argument('--held-scale-acknowledged', action='store_true')
    media.add_argument('--record', type=Path)
    media.add_argument('--distinct-copies', action='store_true',
                        help='stream-copy each title so its bytes differ (distinct scanned titles) instead of hard links')
    media.add_argument('--max-links', type=int, default=20000)
    media.add_argument('--window-seconds', type=int, default=900)
    media.add_argument('--appdata', type=Path)
    media.add_argument('--out', type=Path, required=True)
    media.add_argument('--execute', action='store_true')

    bootstrap = commands.add_parser('bootstrap', help='start/stop the isolated server and scan the real library')
    bootstrap.add_argument('--server-binary', type=Path, required=True)
    bootstrap.add_argument('--appdata', type=Path, required=True)
    bootstrap.add_argument('--media-facts', type=Path, required=True)
    bootstrap.add_argument('--capabilities', type=Path)
    bootstrap.add_argument('--port', type=int, default=32665)
    bootstrap.add_argument('--owner-username', default='p265-owner')
    bootstrap.add_argument('--window-seconds', type=int, default=900)
    bootstrap.add_argument('--bytes-per-second', type=int, default=500_000)
    bootstrap.add_argument('--ready-timeout', type=float, default=120)
    bootstrap.add_argument('--scan-timeout', type=float, default=300)
    bootstrap.add_argument('--stop-timeout', type=float, default=30)
    bootstrap.add_argument('--execute', action='store_true')

    identities = commands.add_parser('identities', help='provision real member identities through the supported API')
    identities.add_argument('--target', required=True)
    identities.add_argument('--owner-credentials', type=Path, required=True)
    identities.add_argument('--capabilities', type=Path, required=True)
    identities.add_argument('--members', type=int, default=100)
    identities.add_argument('--pacing-seconds', type=float, default=1.0)
    identities.add_argument('--username-prefix', default='scale-user')
    identities.add_argument('--custody', type=Path)
    identities.add_argument('--out', type=Path, required=True)
    identities.add_argument('--execute', action='store_true')

    manifest = commands.add_parser('manifest', help='assemble the private load manifest')
    manifest.add_argument('--identities', type=Path, required=True)
    manifest.add_argument('--prep-receipt', type=Path, required=True)
    manifest.add_argument('--workload', type=Path, required=True)
    manifest.add_argument('--out', type=Path, required=True)
    manifest.add_argument('--execute', action='store_true')

    proposal = commands.add_parser('proposal', help='print the replacement300k real-scan plan')
    proposal.add_argument('--out', type=Path)

    args = parser.parse_args()
    if args.command == 'media':
        command_media(args, parser)
    elif args.command == 'bootstrap':
        command_bootstrap(args, parser)
    elif args.command == 'identities':
        command_identities(args, parser)
    elif args.command == 'manifest':
        command_manifest(args, parser)
    else:
        command_proposal(args, parser)


if __name__ == '__main__':
    def interrupted(signum, _frame):
        raise SystemExit(128 + signum)
    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGINT, interrupted)
    main()
