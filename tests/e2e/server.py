#!/usr/bin/env python3
"""Start a disposable devtrust server for hosted E2E jobs.

The media cache contains generated media only. Server state and credentials are
never cached or uploaded. The child command runs while the server is alive.
"""
import argparse
import importlib.util
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

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
        fixture.generate(media)
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
            state = api.get('/v1/setup/state')
            if not state['ready']:
                api.post('/v1/setup/finish', {'revision': state['revision']})
            server.scan()
            command = args.command
            if command and command[0] == '--':
                command = command[1:]
            if command:
                return subprocess.call(command, env={**os.environ, 'PORTICO_E2E_SERVER_URL': server.base})
            print('PASS: disposable devtrust server, password-free sign-in and synthetic library scans', flush=True)
            return 0
        finally:
            server.stop()


if __name__ == '__main__':
    sys.exit(main())
