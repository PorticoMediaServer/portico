#!/usr/bin/env python3
"""Run the real decoder sandbox and probe pool on a disposable hosted Linux VM."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import time

from run import classify

ROOT = Path(__file__).resolve().parents[1]
PREFIX = 'github.com/PorticoMediaServer/portico/server/internal/'
REQUIRED = {
    PREFIX + 'mediaexec': [
        'TestSandboxConfinesReadsWritesAndNetwork',
        'TestBurnInFindsFontsSandboxedAndBaseline',
        'TestProbeWorkerRunsSequentialProbesInOneSandbox',
        'TestProbeWorkerCancellationKillsItsSandbox',
        'TestProbeWorkerRefusesOutsideItsPIDNamespace',
    ],
    PREFIX + 'decoder': [
        'TestHostedSandboxRequiresDecoderBridge',
        'TestLiveHLSWritesThroughTheLinuxSandbox',
        'TestLinearOutputCommandKeepsGPUGrantInsideSandbox',
        'TestConfinedBurnInDrawsText',
    ],
}

APT_UPDATE_SECONDS = 90
APT_INSTALL_SECONDS = 240
APT_MIRROR = 'http://archive.ubuntu.com/ubuntu'
UBUNTU_MIRROR = re.compile(r'https?://(?:[A-Za-z0-9-]+\.)*(?:archive|security)\.ubuntu\.com/ubuntu/?')
UBUNTU_MIRROR_LIST = re.compile(r'mirror\+file:/etc/apt/apt-mirrors(-security)?\.txt(?=$|\s)')


def command(args, timeout=180):
    process = subprocess.Popen(args, start_new_session=True)
    try:
        returncode = process.wait(timeout=timeout)
    except subprocess.TimeoutExpired:
        # apt runs as root under sudo; kill its whole group, not just sudo.
        subprocess.run(['sudo', 'kill', '-KILL', '--', '-' + str(process.pid)],
                       check=True, timeout=30)
        process.wait(timeout=30)
        raise
    if returncode:
        raise subprocess.CalledProcessError(returncode, args)


def wait_for_package_processes():
    deadline = time.monotonic() + 30
    while True:
        running = False
        for name in ['apt-get', 'dpkg']:
            result = subprocess.run(['pgrep', '-x', name], stdout=subprocess.DEVNULL, timeout=5)
            if result.returncode not in (0, 1):
                result.check_returncode()
            running |= result.returncode == 0
        if not running:
            return True
        if time.monotonic() >= deadline:
            return False
        time.sleep(1)


def require_hosted_linux():
    # These kernel changes belong only to the job's disposable VM. Never change
    # namespace or AppArmor policy on a developer host or self-hosted runner.
    if (os.environ.get('GITHUB_ACTIONS') != 'true'
            or os.environ.get('RUNNER_ENVIRONMENT') != 'github-hosted'
            or os.environ.get('RUNNER_OS') != 'Linux'):
        raise ValueError('sandbox setup requires a disposable GitHub-hosted Linux runner')


def switch_apt_mirror(root=Path('/etc/apt')):
    require_hosted_linux()
    # Ubuntu 24.04 uses deb822 .sources files; older images use .list files.
    files = [root / 'sources.list', *sorted((root / 'sources.list.d').glob('*.list')),
             *sorted((root / 'sources.list.d').glob('*.sources'))]
    matched = 0
    for path in files:
        if not path.is_file():
            continue
        original = path.read_text()
        updated, count = UBUNTU_MIRROR.subn(
            lambda match: match[0] if '//security.ubuntu.com/ubuntu' in match[0] else APT_MIRROR, original)
        # Hosted 24.04 images can select Azure indirectly through a mirrorlist.
        # Bypass that list, including its separate security-suite variant.
        updated, lists = UBUNTU_MIRROR_LIST.subn(
            lambda match: 'http://security.ubuntu.com/ubuntu' if match[1] else APT_MIRROR, updated)
        matched += count + lists
        if updated != original:
            path.write_text(updated)
        if count or lists:
            effective = sorted(set(UBUNTU_MIRROR.findall(updated)))
            print(path.name + ': effective Ubuntu apt URIs: ' + ', '.join(effective), flush=True)
    if not matched:
        raise ValueError('no Ubuntu apt mirror found; refusing unchanged-mirror retry')
    print('Retrying Ubuntu packages through ' + APT_MIRROR, flush=True)


def setup():
    require_hosted_linux()
    for attempt in range(2):
        try:
            command(['sudo', 'apt-get', '-o', 'Acquire::Retries=3',
                     '-o', 'DPkg::Lock::Timeout=60', 'update'], timeout=APT_UPDATE_SECONDS)
            command(['sudo', 'apt-get', '-o', 'Acquire::Retries=3',
                     '-o', 'DPkg::Lock::Timeout=60', 'install', '-y',
                     '--no-install-recommends', 'bubblewrap', 'ffmpeg'], timeout=APT_INSTALL_SECONDS)
            break
        except (subprocess.TimeoutExpired, subprocess.CalledProcessError):
            if attempt == 1:
                raise
            # sudo's PTY monitor can put apt in a different session. Only this
            # disposable VM may kill all package processes and repair dpkg.
            if not wait_for_package_processes():
                for name in ['apt-get', 'dpkg']:
                    result = subprocess.run(['sudo', 'pkill', '-KILL', '-x', name], timeout=30)
                    if result.returncode not in (0, 1):  # 1: exited before pkill.
                        result.check_returncode()
                if not wait_for_package_processes():
                    raise ValueError('package processes survived cleanup; refusing apt retry')
            command(['sudo', 'dpkg', '--configure', '-a'], timeout=120)
            command(['sudo', '--preserve-env=GITHUB_ACTIONS,RUNNER_ENVIRONMENT,RUNNER_OS',
                     'python3', str(Path(__file__).resolve()), 'apt-mirror'], timeout=30)
            time.sleep(5)
    for key, value in [('kernel.apparmor_restrict_unprivileged_userns', '0'),
                       ('kernel.unprivileged_userns_clone', '1')]:
        if (Path('/proc/sys') / key.replace('.', '/')).exists():
            command(['sudo', 'sysctl', '-w', key + '=' + value], timeout=15)
    # Check as the runner user, using the user, PID, mount and network namespaces
    # the production helpers need. Installing bwrap alone does not prove access.
    command(['bwrap', '--unshare-all', '--die-with-parent', '--ro-bind', '/', '/',
             '--proc', '/proc', '--dev', '/dev', '--cap-drop', 'ALL', '/bin/true'], timeout=15)


def verify(events, returncode):
    if returncode != 0:
        raise ValueError('decoder sandbox suite failed')
    for package, tests in REQUIRED.items():
        own = [event for event in events if event.get('Package') == package]
        if classify(own, tests, returncode, set()) != 'pass':
            raise ValueError(package + ': incomplete or failing sandbox suite')
        for name in tests:
            terminals = [event['Action'] for event in own
                         if event.get('Test') == name and event.get('Action') in ('pass', 'fail', 'skip')]
            if terminals != ['pass']:
                raise ValueError(package + '.' + name + ': must run and pass, got ' + str(terminals))


def test():
    directory = ROOT / 'ci-sandbox-results'
    directory.mkdir(exist_ok=True)
    tags = subprocess.check_output(['python3', 'scripts/release-build-tags.py', '--capability'],
                                   cwd=ROOT, text=True, timeout=30).strip()
    env = dict(os.environ, PORTICO_REQUIRE_DECODER_SANDBOX='1')
    args = ['go', 'test', '-json', '-count=1', '-p=2', '-timeout=5m', '-tags', tags, *REQUIRED]
    result = subprocess.run(args, cwd=ROOT / 'server', env=env, capture_output=True, text=True, timeout=480)
    (directory / 'tests.jsonl').write_text(result.stdout)
    (directory / 'tests.stderr').write_text(result.stderr)
    (directory / 'selection.json').write_text(json.dumps(
        {'head': os.environ.get('GITHUB_SHA'), 'command': args, 'required': REQUIRED}, indent=2) + '\n')
    print(result.stdout, end='')
    print(result.stderr, end='')
    events = [json.loads(line) for line in result.stdout.splitlines()]
    verify(events, result.returncode)
    print('Every required sandbox, live decoder and M4 probe pool test ran and passed.')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['setup', 'test', 'apt-mirror'])
    args = parser.parse_args()
    {'setup': setup, 'test': test, 'apt-mirror': switch_apt_mirror}[args.action]()
