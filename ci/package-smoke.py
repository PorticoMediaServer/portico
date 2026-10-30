#!/usr/bin/env python3
"""Install/start a qualified Linux portable package. Never builds any toolchain."""
import argparse
import os
from pathlib import Path
import socket
import subprocess
import tarfile
import tempfile
import time


def smoke(package):
    with tempfile.TemporaryDirectory(prefix='portico-package-smoke-') as scratch:
        root = Path(scratch); payload = root/'installed'; payload.mkdir()
        with tarfile.open(package) as archive:
            archive.extractall(payload, filter='data')
        launcher = payload/'start-portico.sh'
        for required in [launcher, payload/'portico-server',payload/'manifest.json',payload/'manifest.sig',payload/'web/index.html']:
            if not required.is_file(): raise ValueError('incomplete qualified package: '+required.name)
        with socket.socket() as sock:
            sock.bind(('127.0.0.1',0)); port=sock.getsockname()[1]
        env = dict(os.environ, PORTICO_DATA_DIR=str(root/'data'), PORTICO_BIND=f'127.0.0.1:{port}', PORTICO_DISCOVERY='0')
        with (root/'server.log').open('w') as log:
            process = subprocess.Popen(['sh',str(launcher)],env=env,stdout=log,stderr=log)
            try:
                deadline=time.monotonic()+60
                while time.monotonic()<deadline:
                    if process.poll() is not None: raise RuntimeError('packaged server exited during startup')
                    check=subprocess.run([str(payload/'portico-server'),'--health-check'],env=env,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=5)
                    if check.returncode==0: return
                    time.sleep(.5)
                raise RuntimeError('packaged server failed readiness deadline')
            finally:
                if process.poll() is None:
                    process.terminate()
                    try: process.wait(timeout=10)
                    except subprocess.TimeoutExpired: process.kill(); process.wait()

if __name__=='__main__':
    parser=argparse.ArgumentParser(); parser.add_argument('--package',required=True,type=Path)
    smoke(parser.parse_args().package.resolve())
