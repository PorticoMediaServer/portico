#!/usr/bin/env python3
"""Redeploy a successful hosted main build to the isolated test service.

Usage: python3 tests/e2e/deploy-remote.py ACTIONS_RUN_ID
The caller needs gh access and SSH access to the approved portico-runner alias.
Only the already-built artifact is handled locally; the server's data survives.
"""
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys
import tarfile
import tempfile
import uuid

REMOTE_SCRIPT = r'''import fcntl, hashlib, os, pathlib, subprocess, sys, tarfile, time, urllib.request
commit, digest, incoming = sys.argv[1:]
lock = open('/var/lib/portico-e2e-remote-operator/deploy.lock', 'a')
os.chmod(lock.name, 0o600)
fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
archive = pathlib.Path(incoming)
assert hashlib.sha256(archive.read_bytes()).hexdigest() == digest
root = pathlib.Path('/opt/portico-e2e-remote/releases')
target = root / commit
assert not target.exists(), 'This release already exists; inspect it before redeploying.'
assert subprocess.run(['systemctl', 'is-enabled', 'portico-e2e-remote'], stdout=subprocess.DEVNULL).returncode == 0
with tarfile.open(archive) as bundle:
 for item in bundle.getmembers():
  p = pathlib.PurePosixPath(item.name)
  assert not p.is_absolute() and '..' not in p.parts and not item.issym() and not item.islnk()
 target.mkdir(mode=0o755)
 bundle.extractall(target, filter='data')
subprocess.run(['sha256sum', '-c', 'SHA256SUMS'], cwd=target, check=True, stdout=subprocess.DEVNULL)
assert (target / 'commit.txt').read_text().strip() == commit
unitdir = pathlib.Path('/etc/systemd/system/portico-e2e-remote.service.d')
unitdir.mkdir(mode=0o755, exist_ok=True)
drop = unitdir / 'release.conf'
previous = drop.read_bytes() if drop.exists() else None
replacement = ('[Service]\nExecStart=\nExecStart=' + str(target / 'portico-server') + '\nEnvironment=PORTICO_WEB_DIR=' + str(target / 'web') + '\n').encode()
tmp = unitdir / ('.release-' + commit)
tmp.write_bytes(replacement); tmp.chmod(0o644); os.replace(tmp, drop)
def restart():
 subprocess.run(['systemctl', 'daemon-reload'], check=True)
 subprocess.run(['systemctl', 'restart', 'portico-e2e-remote'], check=True)
try:
 restart(); deadline = time.monotonic() + 150
 while True:
  try:
   with urllib.request.urlopen('http://localhost:32520/v1/system', timeout=5) as response:
    if response.status == 200: break
  except OSError: pass
  if time.monotonic() >= deadline: raise RuntimeError('Updated service did not become ready')
  time.sleep(2)
except BaseException:
 if previous is None: drop.unlink(missing_ok=True)
 else:
  tmp.write_bytes(previous); tmp.chmod(0o644); os.replace(tmp, drop)
 restart()
 raise
finally:
 archive.unlink(missing_ok=True)
print('Updated isolated E2E service to main commit', commit)
'''


def gh(*args):
    return subprocess.check_output(['gh', *args], text=True).strip()


def main():
    if len(sys.argv) != 2 or not sys.argv[1].isdigit():
        raise SystemExit(__doc__)
    run = sys.argv[1]
    meta = json.loads(gh('run', 'view', run, '--repo', 'PorticoMediaServer/portico', '--json',
                         'status,conclusion,event,headBranch,workflowName'))
    if meta != {'status': 'completed', 'conclusion': 'success', 'event': 'workflow_dispatch',
                'headBranch': 'main', 'workflowName': 'E2E remote server build'}:
        raise SystemExit('Expected a successful manually dispatched remote-build workflow on main.')
    head = gh('api', 'repos/PorticoMediaServer/portico/commits/main', '--jq', '.sha')
    if not re.fullmatch(r'[0-9a-f]{40}', head):
        raise SystemExit('Could not verify main commit.')
    with tempfile.TemporaryDirectory(prefix='portico-e2e-deploy-') as scratch:
        subprocess.run(['gh', 'run', 'download', run, '--repo', 'PorticoMediaServer/portico',
                        '--name', 'remote-test-server-main', '--dir', scratch], check=True)
        artifact = Path(scratch) / 'remote-server.tar.gz'
        with tarfile.open(artifact) as bundle:
            names = bundle.getnames()
            for item in bundle.getmembers():
                path = Path(item.name)
                if path.is_absolute() or '..' in path.parts or item.issym() or item.islnk():
                    raise SystemExit('Unsafe archive member.')
            name = './commit.txt' if './commit.txt' in names else 'commit.txt'
            commit = bundle.extractfile(name).read().decode().strip()
        if commit != head:
            raise SystemExit('Artifact is not the current main commit; dispatch a fresh build.')
        digest = hashlib.file_digest(artifact.open('rb'), 'sha256').hexdigest()
        incoming = '/tmp/portico-e2e-' + uuid.uuid4().hex + '.tar.gz'
        subprocess.run(['scp', '-q', str(artifact), 'root@portico-runner:' + incoming], check=True)
        subprocess.run(['ssh', '-o', 'BatchMode=yes', 'root@portico-runner', 'python3', '-',
                        commit, digest, incoming], input=REMOTE_SCRIPT, text=True, check=True)


if __name__ == '__main__':
    main()
