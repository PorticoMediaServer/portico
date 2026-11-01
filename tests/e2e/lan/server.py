#!/usr/bin/env python3
"""Launch an unconfigured local-account server on this hosted runner's LAN IP."""
import importlib.util
import ipaddress
import json
import os
from pathlib import Path
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request


def address():
    interfaces = json.loads(subprocess.check_output(['ip', '-j', '-4', 'addr', 'show', 'dev', 'eth0']))
    private = [ipaddress.ip_network(x) for x in ('10.0.0.0/8', '172.16.0.0/12', '192.168.0.0/16')]
    for info in interfaces[0]['addr_info']:
        value = ipaddress.ip_address(info['local'])
        if any(value in net for net in private):
            return str(value)
    raise RuntimeError('No private IPv4 address on eth0')


def main():
    host = address()
    with socket.socket() as sock:
        sock.bind((host, 0))
        port = sock.getsockname()[1]
    origin = f'http://{host}:{port}'
    os.umask(0o077)
    with tempfile.TemporaryDirectory(prefix='portico-lan-e2e-', dir=os.environ['RUNNER_TEMP']) as tmp:
        root = Path.cwd()
        spec = importlib.util.spec_from_file_location('e2e_server', root / 'tests/e2e/server.py')
        e2e = importlib.util.module_from_spec(spec)
        sys.path.insert(0, str(root / 'tests/e2e'))
        spec.loader.exec_module(e2e)
        media = Path(os.environ['RUNNER_TEMP']) / 'portico-e2e-media'
        if not media.exists():
            with tempfile.TemporaryDirectory(prefix='portico-media-', dir=os.environ['RUNNER_TEMP']) as staging:
                generated = Path(staging) / 'media'
                e2e.fixture.generate(generated, load_seconds=12)
                e2e.augment_media(generated)
                generated.rename(media)
        (Path(tmp) / 'media').symlink_to(media, target_is_directory=True)
        env = {k: v for k, v in os.environ.items() if not k.startswith('PORTICO_')}
        env.update(PORTICO_DATA_DIR=tmp + '/state', PORTICO_BIND=f'{host}:{port}', PORTICO_DISCOVERY='0',
                   PORTICO_WEB_DIR=str(Path('web/dist').resolve()), PORTICO_FFMPEG=shutil.which('ffmpeg'),
                   PORTICO_FFPROBE=shutil.which('ffprobe'))
        with open(tmp + '/server.log', 'wb') as log:
            process = subprocess.Popen([os.environ['PORTICO_E2E_BINARY']], env=env, stdout=log,
                                       stderr=subprocess.STDOUT, start_new_session=True)
            try:
                deadline = time.monotonic() + 150
                opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
                while True:
                    if process.poll() is not None:
                        raise RuntimeError('Server exited during startup')
                    try:
                        with opener.open(origin + '/v1/system', timeout=5) as response:
                            if json.load(response).get('setupRequired'):
                                break
                    except (OSError, urllib.error.URLError):
                        pass
                    if time.monotonic() >= deadline:
                        raise RuntimeError('Server did not become ready')
                    time.sleep(.5)
                child_env = dict(os.environ, PORTICO_E2E_LAN_URL=origin, PORTICO_E2E_SESSION_FILE=tmp + '/session.json')
                result = subprocess.call(['../web/node_modules/.bin/playwright', 'test', '--config', 'lan.config.mjs'],
                                         cwd='tests/e2e/lan', env=child_env)
                if result == 0:
                    class RunningServer:
                        pass
                    server = RunningServer()
                    server.directory = Path(tmp)
                    server.base = origin
                    server.binary = Path(os.environ['PORTICO_E2E_BINARY'])
                    server.api = e2e.fixture.API(origin)
                    server.api.token = json.loads(Path(tmp + '/session.json').read_text())['accessToken']
                    e2e.scan_libraries(server)
                    e2e.provision_member(server)
                return result
            finally:
                try:
                    os.killpg(process.pid, signal.SIGTERM)
                    process.wait(timeout=30)
                except ProcessLookupError:
                    pass
                except subprocess.TimeoutExpired:
                    os.killpg(process.pid, signal.SIGKILL)
                    process.wait()


if __name__ == '__main__':
    sys.exit(main())
