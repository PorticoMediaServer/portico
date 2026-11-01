#!/usr/bin/env python3
"""Start a disposable devtrust server for hosted E2E jobs.

The media cache contains generated media only. Server state and credentials are
never cached or uploaded. The child command runs while the server is alive.
"""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import sys
import tempfile
from augment_media import generate as augment_media

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('portico_e2e_server', ROOT / 'scripts/e2e-server.py')
fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixture)


class DevServer(fixture.Exercise):
    def start(self):
        # The production exercise's state name cannot activate the dev hook.
        env = {k: v for k, v in os.environ.items() if not k.startswith('PORTICO_')}
        self.base = f'http://127.0.0.1:{fixture.port()}'
        env.update(PORTICO_DATA_DIR=str(self.directory / 'e2e-state'),
                   PORTICO_BIND=self.base.split('//')[1], PORTICO_DISCOVERY='0',
                   PORTICO_FFMPEG=shutil.which('ffmpeg'), PORTICO_FFPROBE=shutil.which('ffprobe'))
        self.log = open(self.directory / 'server.log', 'wb')
        self.process = subprocess.Popen([str(self.binary)], cwd=ROOT / 'server', env=env,
                                        stdout=self.log, stderr=subprocess.STDOUT, start_new_session=True)
        self.api = fixture.API(self.base)

        def ready():
            if self.process.poll() is not None:
                raise RuntimeError('dev server exited before readiness')
            try:
                return self.api.get('/v1/system')
            except (OSError, AssertionError):
                return False
        fixture.wait(ready, 120)


def provision_member(server):
    password = secrets.token_urlsafe(24) + '-Aa1!'
    pin = f'{secrets.randbelow(10000):04d}'
    server.api.post('/v1/direct/members', {
        'username': 'e2e-member', 'password': password, 'name': 'E2E Member',
        'allowedLibraries': list(server.libs.values()), 'disabled': False,
        'expectedRevision': 0,
    }, (201,))
    member = fixture.API(server.base)
    sign_in = member.post('/v1/direct/sign-in', {'username': 'e2e-member', 'password': password})
    member.token = sign_in['session']['accessToken']
    snapshot = member.post('/v1/direct/profiles', {'name': 'Restricted', 'art': ''}, (201,))
    profile = next(p for p in snapshot['profiles'] if p['name'] == 'Restricted')
    path = '/v1/direct/profiles/' + profile['id']
    member.call('PATCH', path, {'expectedRevision': profile['revision'], 'pin': pin})
    restriction = member.get(path + '/restrictions')
    member.call('PUT', path + '/restrictions', {
        'expectedRevision': restriction['revision'], 'ratingSystem': 'mpaa',
        'maximumAgeRating': 'PG-13', 'allowUnrated': True,
        'blockedLabels': [], 'allowDownloads': False, 'allowLiveTv': False,
        'allowDvr': False, 'allowWatchTogether': False,
    })
    credentials = server.directory / 'client-credentials.json'
    credentials.write_text(json.dumps({'member': {'username': 'e2e-member', 'password': password},
                                       'restrictedProfile': {'id': profile['id'], 'pin': pin}}))
    return credentials


def scan_libraries(server):
    server.libs = {}
    libraries = [('Movies', 'movie', 'movies', 16), ('Shows', 'tv', 'shows', 2),
                 ('Anime', 'anime', 'shows', 1), ('Music', 'music', 'albums', 3),
                 ('Books', 'audiobook', 'books', 1), ('Reading', 'book', None, None),
                 ('Comics', 'comic', None, None)]
    for name, kind, pivot, count in libraries:
        api = server.api
        library = api.post('/v1/libraries', {'name': name, 'kind': kind,
                           'path': str(server.directory / 'media' / name),
                           'metadataAgent': 'local'}, (201,))
        server.libs[name] = library['id']
        job = api.post('/v1/libraries/' + library['id'] + '/scans', expected=(202,))
        def done():
            current = api.get('/v1/admin/ingestion/jobs/' + job['id'])
            if current['status'] in ('failed', 'cancelled'):
                raise RuntimeError(f'{name} scan failed')
            return current['status'] in ('complete', 'complete_with_warnings')
        fixture.wait(done, 120)
        if pivot:
            def published():
                page, _ = api.call('POST', '/v1/libraries/' + library['id'] + '/browse',
                                   {'pivot': pivot, 'limit': 100}, expected=(200, 503))
                if page.get('error', {}).get('code') == 'visibility_building':
                    return False
                if 'entries' not in page:
                    raise RuntimeError(f'{name} browse failed: {page}')
                return len(page['entries']) == count
            fixture.wait(published, 120)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--media-cache', type=Path, required=True)
    parser.add_argument('command', nargs=argparse.REMAINDER)
    args = parser.parse_args()
    media = args.media_cache.resolve()
    binary = args.binary.resolve()
    if not binary.is_file():
        parser.error('server binary does not exist')
    if media == ROOT or ROOT in media.parents:
        parser.error('media cache must be outside the checkout')
    os.umask(0o077)
    if not media.exists():
        media.parent.mkdir(parents=True, exist_ok=True)
        # Publish only a complete fixture; a failed generator must not leave a
        # cache that appears usable in the next attempt.
        with tempfile.TemporaryDirectory(prefix='portico-media-', dir=media.parent) as tmp:
            generated = Path(tmp) / 'media'
            fixture.generate(generated, load_seconds=12)
            augment_media(generated)
            generated.rename(media)
    if not (media / 'Movies/Arrival (2016)/Arrival (2016) - 1080p.mp4').is_file():
        raise RuntimeError('incomplete media cache')
    with tempfile.TemporaryDirectory(prefix='portico-e2e-', dir=os.environ.get('RUNNER_TEMP')) as tmp:
        directory = Path(tmp)
        (directory / 'media').symlink_to(media, target_is_directory=True)
        server = DevServer(directory, binary)
        try:
            server.start()
            api = server.api
            user = api.get('/v1/dev/e2e')['username']
            signed = api.post('/v1/dev/e2e/sign-in', {'username': user})
            api.token = signed['session']['accessToken']
            scan_libraries(server)
            credentials = provision_member(server)
            command = args.command
            if command and command[0] == '--':
                command = command[1:]
            if command:
                return subprocess.call(command, env={**os.environ, 'PORTICO_E2E_SERVER_URL': server.base,
                                                       'PORTICO_E2E_CREDENTIALS_FILE': str(credentials)})
            print('PASS: disposable devtrust server, password-free sign-in and synthetic library scans', flush=True)
            return 0
        finally:
            server.stop()


if __name__ == '__main__':
    sys.exit(main())
