#!/usr/bin/env python3
"""100-file tiny acceptance for the P2-65 ingest orchestrator.

Starts an isolated current server on loopback (default 32666) with a fresh
private appdata, runs the ordinary setup and creates the three real libraries
through the supported API, then lets `orchestrate.py` generate 100 distinct
real files, scan them, and reconcile the API count. It stops the server in a
finally block and never touches another fixture.

This is the tiny rehearsal only. 300k stays gated. Plan is the default.
"""
import argparse
import json
import os
import pathlib
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid

HERE = pathlib.Path(__file__).resolve().parent
STOP_BYTES = 7 * 1024 ** 3
HARD_BYTES = 8 * 1024 ** 3


def refuse(message, code=2):
    print(json.dumps({'state': 'REFUSED', 'reason': message}), file=sys.stderr)
    raise SystemExit(code)


def private_dir(path, create=False):
    path = pathlib.Path(path).absolute()
    if create:
        path.mkdir(parents=True, exist_ok=True, mode=0o700)
    if path.is_symlink() or not path.is_dir() or path.stat().st_uid != os.getuid() or path.stat().st_mode & 0o077:
        refuse(f'expected a private 0700 directory: {path}')
    return path


def private_file(path):
    path = pathlib.Path(path)
    if path.is_symlink() or not path.is_file() or path.stat().st_uid != os.getuid() or path.stat().st_mode & 0o777 != 0o600:
        refuse(f'expected an operator-owned 0600 regular file: {path}')
    return path


class Client:
    def __init__(self, base, installation):
        self.base = base.rstrip('/')
        self.installation = installation
        self.origin = None
        self.token = ''

    def call(self, method, path, body=None, ok=(200,), timeout=120):
        data = None if body is None else json.dumps(body).encode()
        request = urllib.request.Request(self.base + path, data=data, method=method)
        request.add_header('Accept', 'application/json')
        request.add_header('X-Portico-Installation-Id', self.installation)
        if self.origin:
            request.add_header('Origin', self.origin)
        if data is not None:
            request.add_header('Content-Type', 'application/json')
        if self.token:
            request.add_header('Authorization', 'Bearer ' + self.token)
        try:
            with urllib.request.urlopen(request, timeout=timeout) as response:
                payload = response.read()
                return json.loads(payload) if payload else None
        except urllib.error.HTTPError as error:
            raise RuntimeError(f'{method} {path} -> HTTP {error.code}') from None


def wait_ready(base, seconds):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen(base + '/v1/system', timeout=3) as response:
                if response.status == 200:
                    return json.loads(response.read())
        except (urllib.error.URLError, OSError, ValueError):
            pass
        time.sleep(0.25)
    return None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--server-binary', required=True)
    parser.add_argument('--appdata', required=True)
    parser.add_argument('--scratch', required=True, help='private media root for generated phases')
    parser.add_argument('--marker', required=True)
    parser.add_argument('--facts', required=True)
    parser.add_argument('--credentials', required=True)
    parser.add_argument('--control', required=True)
    parser.add_argument('--port', type=int, default=32666)
    parser.add_argument('--ffmpeg', default='/usr/bin/ffmpeg')
    parser.add_argument('--ffprobe', default='/usr/bin/ffprobe')
    parser.add_argument('--execute', action='store_true')
    args = parser.parse_args()
    base = f'http://127.0.0.1:{args.port}'
    plan = {'state': 'PLAN', 'base': base, 'server': str(pathlib.Path(args.server_binary)), 'control': str(pathlib.Path(args.control)),
            'appdata': str(pathlib.Path(args.appdata)), 'scratch': str(pathlib.Path(args.scratch)),
            'files': 100, 'libraries': ['movie', 'tv', 'music'], 'execute': False,
            'stopBytes': STOP_BYTES, 'hardBytes': HARD_BYTES}
    if not args.execute:
        print(json.dumps(plan))
        return
    if 'CI' in os.environ or 'GITHUB_ACTIONS' in os.environ:
        refuse('live tiny bootstrap is prohibited in CI')
    binary = pathlib.Path(args.server_binary)
    if not binary.is_file() or not os.access(binary, os.X_OK):
        refuse(f'server binary is not executable: {binary}')
    appdata = private_dir(args.appdata, create=True)
    if any(appdata.iterdir()):
        refuse(f'appdata must be empty; never reuse: {appdata}')
    scratch = private_dir(args.scratch, create=True)
    marker = pathlib.Path(args.marker)
    private_dir(marker.parent, create=True)
    facts = pathlib.Path(args.facts)
    credentials = pathlib.Path(args.credentials)
    log = appdata / 'bootstrap.log'
    runtime_tmp = private_dir(appdata.parent / (appdata.name + '-runtime-tmp'), create=True)
    environment = dict(os.environ)
    environment.update({'PORTICO_DATA_DIR': str(appdata), 'PORTICO_BIND': f'127.0.0.1:{args.port}',
                        'PORTICO_SETUP_HOSTS': f'localhost:{args.port},127.0.0.1:{args.port}',
                        'PORTICO_FFMPEG': args.ffmpeg, 'PORTICO_FFPROBE': args.ffprobe,
                        'GOMAXPROCS': '2', 'HOME': str(appdata), 'TMPDIR': str(runtime_tmp)})
    handle = open(log, 'wb')
    os.chmod(log, 0o600)
    process = subprocess.Popen([str(binary)], stdout=handle, stderr=handle, stdin=subprocess.DEVNULL,
                               env=environment, cwd=str(appdata), start_new_session=True)
    try:
        ready = wait_ready(base, 120)
        if ready is None:
            refuse(f'server never answered /v1/system; see {log}')
        installation = uuid.uuid4().hex
        client = Client(base, installation)
        client.origin = base
        browser = client.call('POST', '/v1/setup/browser', {})
        username = 'p265-ingest-owner'
        import secrets
        password = secrets.token_urlsafe(24)
        fd = os.open(credentials, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, 'w') as output:
            json.dump({'username': username, 'password': password, 'installationId': installation,
                       'base': base}, output)
        client.call('POST', '/v1/setup', {'setupToken': browser['setupToken'], 'requestId': str(uuid.uuid4()),
                                          'name': 'P2-65 ingest tiny', 'authMode': 'local', 'interactive': True,
                                          'username': username, 'password': password}, ok=(200, 201))
        signed = client.call('POST', '/v1/direct/sign-in', {'username': username, 'password': password})
        session = signed.get('session', signed)
        client.token = session['accessToken']
        profiles = signed.get('profiles') or []
        primary = next((entry for entry in profiles if entry.get('primary')), profiles[0])
        selected = client.call('POST', '/v1/direct/profiles/' + primary['id'] + '/select',
                               {'installationId': installation, 'trust': False})
        client.token = selected['session']['accessToken']
        libraries = {}
        for kind, folder, label in (('movie', 'movies', 'P2-65 Movies'), ('tv', 'tv', 'P2-65 TV'),
                                    ('music', 'music', 'P2-65 Music')):
            root = scratch / folder
            root.mkdir(parents=True, exist_ok=True)
            created = client.call('POST', '/v1/libraries', {'name': label, 'kind': kind,
                                                            'path': str(root), 'metadataAgent': 'local'},
                                  ok=(200, 201))
            libraries[kind] = (created.get('library', created)).get('id')
        command = [sys.executable, str(HERE / 'orchestrate.py'), '--base', base,
                   '--credentials', str(credentials), '--media', str(HERE / 'media.py'),
                   '--scratch', str(scratch), '--facts', str(facts), '--marker', str(marker),
                   '--control', str(pathlib.Path(args.control)), '--appdata', str(appdata),
                   '--generation', 'template', '--total', '100', '--phase-size', '100',
                   '--baseline-seconds', '0', '--after-seconds', '0', '--resident-rounds', '3', '--tiny', '--execute']
        for kind in ('movie', 'tv', 'music'):
            command += [f'--library-{kind if kind != "movie" else "movies"}', libraries[kind]]
        attempt = subprocess.run(command, capture_output=True, text=True, timeout=1800)
        print(attempt.stdout)
        print(attempt.stderr, file=sys.stderr)
        if attempt.returncode != 0:
            raise SystemExit(attempt.returncode)
        records = [json.loads(line) for line in marker.read_text().splitlines() if line.strip()]
        end = next((record for record in reversed(records) if record.get('event') == 'fixture-end'), None)
        accepted = (end or {}).get('accepted')
        if accepted != 100:
            refuse(f'API accepted {accepted} of 100 real files', 3)
        print(json.dumps({'state': 'ACCEPTED', 'accepted': accepted, 'marker': str(marker),
                          'facts': str(facts), 'credentials': str(credentials), 'base': base}))
    finally:
        if process.poll() is None:
            process.send_signal(signal.SIGTERM)
            try:
                process.wait(timeout=30)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=10)
        handle.close()


if __name__ == '__main__':
    main()
