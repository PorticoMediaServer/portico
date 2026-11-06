#!/usr/bin/env python3
"""Run the real decoder sandbox and probe pool on a disposable hosted Linux VM."""
import argparse
import json
import os
from pathlib import Path
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


def setup():
    # These kernel changes belong only to the job's disposable VM. Never change
    # namespace or AppArmor policy on a developer host or self-hosted runner.
    if (os.environ.get('GITHUB_ACTIONS') != 'true'
            or os.environ.get('RUNNER_ENVIRONMENT') != 'github-hosted'
            or os.environ.get('RUNNER_OS') != 'Linux'):
        raise ValueError('sandbox setup requires a disposable GitHub-hosted Linux runner')
    for attempt in range(2):
        try:
            command(['sudo', 'apt-get', '-o', 'Acquire::Retries=3',
                     '-o', 'DPkg::Lock::Timeout=60', 'update'], timeout=300)
            command(['sudo', 'apt-get', '-o', 'Acquire::Retries=3',
                     '-o', 'DPkg::Lock::Timeout=60', 'install', '-y',
                     '--no-install-recommends', 'bubblewrap', 'ffmpeg'], timeout=600)
            break
        except (subprocess.TimeoutExpired, subprocess.CalledProcessError):
            if attempt == 1:
                raise
            time.sleep(5)
    for key, value in [('kernel.apparmor_restrict_unprivileged_userns', '0'),
                       ('kernel.unprivileged_userns_clone', '1')]:
        if (Path('/proc/sys') / key.replace('.', '/')).exists():
            command(['sudo', 'sysctl', '-w', key + '=' + value])
    # Check as the runner user, using the user, PID, mount and network namespaces
    # the production helpers need. Installing bwrap alone does not prove access.
    command(['bwrap', '--unshare-all', '--die-with-parent', '--ro-bind', '/', '/',
             '--proc', '/proc', '--dev', '/dev', '--cap-drop', 'ALL', '/bin/true'])


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
    parser.add_argument('action', choices=['setup', 'test'])
    args = parser.parse_args()
    (setup if args.action == 'setup' else test)()
