#!/usr/bin/env python3
"""Explicit fresh Contabo setup. Defaults to a no-network plan. Never invoked by CI."""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import time
import urllib.request

HOME = Path('/home/runner')
WORK = HOME / 'work/test-server-v3'
MEDIA = HOME / 'portico-test-library-v3'
FIXTURE_PIN = '8f43a827b'


def run(argv):
    subprocess.run([str(x) for x in argv], check=True)


def atomic(path, value):
    temp = path.with_name(path.name + '.tmp')
    temp.write_text(value)
    temp.chmod(0o600)
    temp.replace(path)


def plain(path):
    path = path.absolute()
    for component in [path, *path.parents]:
        if component.is_symlink():
            raise ValueError('Symlinked operator path refused')
    return path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--stage', choices=['host', 'provision'], required=True)
    parser.add_argument('--execute', action='store_true')
    parser.add_argument('--confirm', help='Literal: RESET-CONTABO-TEST-SERVER')
    parser.add_argument('--build', type=Path)
    parser.add_argument('--commit', help='Full green-main SHA verified in GitHub Actions')
    parser.add_argument('--fixture-source', type=Path, required=True)
    parser.add_argument('--manifest', type=Path)
    parser.add_argument('--credentials', type=Path)
    parser.add_argument('--report', type=Path)
    media_mode = parser.add_mutually_exclusive_group()
    media_mode.add_argument('--resume-staged-sha256', help='Explicitly resume inspected generated media with this manifest SHA256')
    media_mode.add_argument('--reuse-fresh-media-sha256', help='Rotate appdata only, retaining an inspected freshly generated media tree with this manifest SHA256')
    args = parser.parse_args()
    if not args.execute:
        print(json.dumps({'mode': 'plan', 'stage': args.stage, 'fixturePin': FIXTURE_PIN,
                          'steps': ['verify pinned fixture source and green build', 'generate fresh media in private staging',
                                    'stop only test server/live/health units', 'retire old appdata and media',
                                    'start fresh appdata on verified build', 'real API bootstrap, accounts, scans and state',
                                    'separate real Hosted registration/claim, then read back readiness'],
                          'hostedClaim': 'required separate authenticated real API step; never bypass email verification'}))
        return
    if 'CI' in os.environ or 'GITHUB_ACTIONS' in os.environ:
        parser.error('Reset/provision is prohibited in CI')
    source = plain(args.fixture_source)
    if (source / '.fixture-revision').read_text().strip() != FIXTURE_PIN:
        parser.error('Fixture source must be the reviewed pinned export')
    generator = source / 'fixtures/test-library'
    if args.stage == 'provision':
        if not args.credentials or not args.manifest or not args.report:
            parser.error('Provision requires private credentials, manifest and report paths')
        if args.credentials.stat().st_mode & 0o777 != 0o600 or args.credentials.stat().st_uid != os.getuid():
            parser.error('Credentials must be operator-owned mode 0600')
        # No credentials travel to the VPS. Existing private tunnel is required.
        run(['python3', generator / 'seed_state.py', '--base', 'http://127.0.0.1:49300',
             '--root', str(MEDIA), '--manifest', args.manifest, '--credentials', args.credentials,
             '--live-base', 'http://127.0.0.1:32511/', '--scan-timeout', '1800', '--report', args.report])
        return
    if socket.gethostname().split('.')[0] != 'portico-runner' or Path.home() != HOME:
        parser.error('Host reset is only for the runner account on Contabo portico-runner')
    if args.confirm != 'RESET-CONTABO-TEST-SERVER' or not args.build or not args.commit:
        parser.error('Host reset needs literal confirmation, build directory and green commit')
    build = plain(args.build)
    if build.parent != HOME / 'work/portico-v3-walks/builds':
        parser.error('Only an existing qualified walk build is admitted')
    manifest = json.loads((build / 'manifest.json').read_text())
    if manifest.get('commit') != args.commit or len(args.commit) != 40 or not (build / 'portico-server').is_file():
        parser.error('Build manifest does not match green main')
    if not manifest.get('generatedClean'):
        parser.error('Generated overlay must be reconciled before this fresh setup')
    health_source = Path(__file__).with_name('test-server-health.py').read_text()
    state = plain(WORK / 'state')
    media = plain(MEDIA)
    if shutil.disk_usage(WORK).free < 20 * 1024**3:
        parser.error('Need at least 20 GiB free')
    task = HOME / 'astra-scratch/P2-53'
    staged = task / 'fresh-media'
    reuse = args.reuse_fresh_media_sha256
    resume = args.resume_staged_sha256
    inspected = reuse or resume
    if inspected:
        if len(inspected) != 64 or any(c not in '0123456789abcdef' for c in inspected):
            parser.error('Resume requires a lowercase SHA256')
        inspected_media = media if reuse else staged
        if not inspected_media.is_dir() or inspected_media.is_symlink() or hashlib.sha256((inspected_media / 'manifest.json').read_bytes()).hexdigest() != inspected:
            parser.error('Inspected staged manifest does not match; no activation attempted')
        if reuse and staged.exists():
            parser.error('Unexpected staging tree exists; inspect it before retaining active fresh media')
    elif staged.exists():
        parser.error('Fresh media staging already exists; inspect it before explicitly resuming')
    else:
        staged.mkdir(mode=0o700)
    python = WORK / 'runtime/venv/bin/python3'
    ffmpeg = HOME / 'work/fixtures/portico-v3/runtime/bundle/bin/ffmpeg'
    # The generator hardlinks its procedural Dolby Vision inputs. Copy source
    # inputs first: reusing them would change ctime on the still-active/retired
    # library's shared inodes. Remove this copy before new scans can observe it.
    isolated_source = task / 'reset-source'
    if isolated_source.exists():
        parser.error('Isolated reset source exists; reconcile the prior attempt')
    if not inspected:
        shutil.copytree(source, isolated_source)
        try:
            run([python, isolated_source / 'fixtures/test-library/generate.py', '--root', staged, '--ffmpeg', ffmpeg])
        finally:
            shutil.rmtree(isolated_source)
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ')
    unit = HOME / '.config/systemd/user/portico-test-server.service'
    deployment = json.loads((WORK / 'deployment.json').read_text())
    old_unit = unit.read_text()
    old_build = deployment['build']
    if old_build not in old_unit or '--ro-bind /home/runner/work/fixtures/portico-v3/runtime/bundle /opt/portico-ffmpeg' not in old_unit:
        parser.error('Existing qualified unit changed; inspect before resetting')
    bindings = [line for line in old_unit.splitlines() if line.startswith('Environment=PORTICO_BIND=')]
    allowed_bindings = {'Environment=PORTICO_BIND=66.94.102.39:32500',
                        'Environment=PORTICO_BIND=127.0.0.1:32510',
                        'Environment=PORTICO_BIND=0.0.0.0:32500',
                        'Environment=PORTICO_BIND=:32500'}
    if len(bindings) != 1 or bindings[0] not in allowed_bindings:
        parser.error('Unknown managed listener; inspect before resetting')
    # Stop the watchdog and socket activation before the server. Otherwise a
    # last health probe can activate the proxy's Requires=server dependency and
    # cancel the stop job while a reset is trying to quiesce the old process.
    run(['systemctl', '--user', 'stop', 'portico-test-health.timer', 'portico-test-health.service'])
    if (unit.parent / 'portico-test-local.socket').is_file():
        run(['systemctl', '--user', 'disable', '--now', 'portico-test-local.socket'])
    if (unit.parent / 'portico-test-local.service').is_file():
        run(['systemctl', '--user', 'stop', 'portico-test-local.service'])
    run(['systemctl', '--user', 'stop', 'portico-test-server.service', 'portico-test-live.service'])
    if subprocess.run(['systemctl', '--user', 'is-active', '--quiet', 'portico-test-server.service']).returncode == 0:
        raise SystemExit('Old server is still active; no state was replaced')
    # Preserve one private rollback while making the active paths genuinely fresh.
    retired_state = WORK / ('state-before-P2-53-' + stamp)
    retired_media = HOME / ('portico-test-library-before-P2-53-' + stamp)
    atomic(task / 'rollback.service', old_unit)
    atomic(task / 'rollback-health.py', (WORK / 'health.py').read_text())
    state.rename(retired_state)
    if not reuse:
        media.rename(retired_media)
        staged.rename(media)
    state.mkdir(mode=0o700)
    for name in ['data', 'cache', 'transcode', 'tmp', 'recordings']:
        (state / name).mkdir(mode=0o700)
    # Use the product default dual-stack socket. An IPv4-only override also
    # deliberately excludes IPv6 from topology publication.
    atomic(unit, old_unit.replace(old_build, str(build)).replace(bindings[0], 'Environment=PORTICO_BIND=:32500'))
    atomic(WORK / 'health.py', health_source)
    run(['systemctl', '--user', 'daemon-reload'])
    run(['systemctl', '--user', 'start', 'portico-test-live.service', 'portico-test-server.service'])
    ready = False
    for _ in range(120):
        try:
            with urllib.request.urlopen('http://127.0.0.1:32500/health/ready', timeout=2) as response:
                ready = response.status == 200
            if ready:
                break
        except (OSError, TimeoutError):
            pass
        time.sleep(1)
    receipt = {'commit': args.commit, 'build': str(build), 'state': str(state), 'media': str(media),
               'ready': ready, 'provisioned': False, 'hostedClaimed': False,
               'retiredState': str(retired_state), 'retiredMedia': None if reuse else str(retired_media), 'resetAt': stamp,
               'listener': ':32500', 'localProxyRequired': False,
               'freshMediaReused': bool(reuse), 'inspectedMediaManifestSHA256': inspected}
    atomic(WORK / 'deployment.json', json.dumps(receipt, indent=2) + '\n')
    if not ready:
        raise SystemExit('Fresh startup failed; rollback paths retained, no readiness claim')
    run(['systemctl', '--user', 'start', 'portico-test-health.timer'])
    print(json.dumps({'freshStartup': True, 'commit': args.commit, 'accountsAndHostedClaimStillRequired': True}))


if __name__ == '__main__':
    main()
