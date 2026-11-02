#!/usr/bin/env python3
"""Install the reviewed ORAS archive, verifying bytes before extraction."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import subprocess
import tarfile
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def digest(path):
    with path.open('rb') as stream: return hashlib.file_digest(stream, 'sha256').hexdigest()


def install(directory):
    pin = json.loads((ROOT / 'third_party/ffmpeg/cache-tools.lock.json').read_text())['oras']
    system = platform.system().lower()
    architecture = {'x86_64': 'amd64', 'aarch64': 'arm64', 'arm64': 'arm64'}.get(platform.machine())
    target = system + '-' + str(architecture)
    expected = pin['archives'][target]
    if not re.fullmatch('[a-f0-9]{64}', expected): raise ValueError('invalid ORAS archive pin')
    directory.mkdir(parents=True, exist_ok=True)
    archive = directory / 'oras.tar.gz'
    url = 'https://github.com/oras-project/oras/releases/download/v' + pin['version'] + '/oras_' + pin['version'] + '_' + target.replace('-', '_') + '.tar.gz'
    for attempt in range(5):
        try:
            with urllib.request.urlopen(url, timeout=60) as source, archive.open('wb') as output:
                size = 0
                while data := source.read(1024**2):
                    size += len(data)
                    if size > 20 * 1024**2: raise ValueError('ORAS download size bound')
                    output.write(data)
            break
        except (OSError, TimeoutError):
            if attempt == 4: raise
            time.sleep((10, 20, 40, 50)[attempt])
    if digest(archive) != expected: raise ValueError('ORAS archive SHA-256 changed')
    with tarfile.open(archive, 'r:gz') as source:
        members = source.getmembers()
        if len(members) > 10 or any(not m.isfile() or m.name not in ('oras', 'LICENSE') or m.size > 30 * 1024**2 for m in members):
            raise ValueError('unexpected ORAS archive inventory')
        if sorted(m.name for m in members) != ['LICENSE', 'oras']: raise ValueError('ORAS executable/license required')
        source.extractall(directory, filter='data')
    binary = directory / 'oras'; binary.chmod(0o755)
    version = subprocess.check_output([binary, 'version'], text=True)
    if not re.search(r'(?m)^Version:\s+' + re.escape(pin['version']) + r'\s*$', version):
        raise ValueError('installed ORAS version differs from pin')
    receipt = dict(version=pin['version'], platform=target, archiveURL=url, archiveSHA256=expected,
                   binarySHA256=digest(binary), qualified=False, release=False)
    (directory / 'receipt.json').write_text(json.dumps(receipt, indent=2) + '\n')
    archive.unlink()
    return receipt


if __name__ == '__main__':
    p = argparse.ArgumentParser(description=__doc__); p.add_argument('action', choices=('install', 'login', 'logout'))
    p.add_argument('--directory', type=Path, required=True)
    a = p.parse_args()
    if a.directory.is_symlink(): raise ValueError('owned cache tool directory required')
    if a.action == 'install': print(json.dumps(install(a.directory)))
    elif a.action == 'login':
        subprocess.run([a.directory / 'oras', 'login', 'ghcr.io', '--username', os.environ['GITHUB_ACTOR'],
                        '--password-stdin', '--registry-config', a.directory / 'auth.json'],
                       input=os.environ['GH_TOKEN'].encode(), check=True)
    else:
        # Credentials are scoped to this disposable job, never native auth state.
        (a.directory / 'auth.json').unlink(missing_ok=True)
