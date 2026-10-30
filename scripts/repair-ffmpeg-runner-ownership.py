#!/usr/bin/env python3
"""Restore ownership only inside this lane's exact temporary runner job."""
import argparse
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile


def validate_owned_job_path(job_root):
    job_root = Path(job_root)
    if (job_root.is_symlink() or job_root.resolve() != job_root or
            job_root.parent.name != 'hardware-r29' or job_root.parent.parent.name != 'jobs' or
            not re.fullmatch(r'[0-9a-f]{12}-[0-9a-f]{8}', job_root.name)):
        raise ValueError('Ownership repair must stay in this lane exact runner job')
    return job_root


def restore_job_ownership(job_root, image):
    job_root = validate_owned_job_path(job_root)
    if not job_root.exists():
        return
    subprocess.run(['docker', 'run', '--rm', '--pull', 'never', '--name', 'portico-hardware-r29-owner-repair',
        '--label', 'portico.lane=hardware-r29', '--network', 'none', '--user', '0',
        '--cpus', '0.5', '--memory', '128m', '--memory-swap', '128m',
        '-v', str(job_root) + ':/job', image, 'bash', '-c',
        'chown -hRP "$(stat -c %u:%g /job)" /job'], check=True, timeout=60)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--runner-source-commit', required=True)
    parser.add_argument('--remove-owned-job', required=True)
    parser.add_argument('--owned-source-commit', required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    if (root.name != 'source' or root.parents[2].name != 'jobs' or
            not re.fullmatch(r'[0-9a-f]{40}', args.runner_source_commit) or
            not re.fullmatch(re.escape(args.runner_source_commit[:12]) + r'-[0-9a-f]{8}', root.parent.name) or
            not re.fullmatch(r'[0-9a-f]{40}', args.owned_source_commit) or
            not re.fullmatch(re.escape(args.owned_source_commit[:12]) + r'-[0-9a-f]{8}', args.remove_owned_job)):
        raise ValueError('Use an exact published runner job and an exact recorded owned failed job')
    target = root.parents[3] / 'jobs/hardware-r29' / args.remove_owned_job
    if target == root.parent:
        raise ValueError('An active repair cannot remove its own running source')
    refused = []
    with tempfile.TemporaryDirectory(prefix='ownership-guards-', dir=os.environ['TMPDIR']) as tmp:
        link = Path(tmp) / 'jobs/hardware-r29/000000000000-00000000'
        link.parent.mkdir(parents=True)
        link.symlink_to('/etc', target_is_directory=True)
        for name, path in [('outside-job', Path('/etc')),
                           ('neighbor-lane', root.parents[3] / 'jobs/other-lane' / args.remove_owned_job),
                           ('symlink-target', link)]:
            try:
                validate_owned_job_path(path)
            except ValueError:
                refused.append(name)
            else:
                raise ValueError('Ownership path guard accepted ' + name)
    active = subprocess.check_output(['docker', 'ps', '-q', '--filter', 'label=portico.lane=hardware-r29'], text=True)
    if active.strip():
        print('REFUSED: an owned hardware container is still active; no cleanup race', flush=True)
        return 75
    lock = json.loads((root / 'third_party/ffmpeg/sources.lock.json').read_text())
    if target.exists():
        restore_job_ownership(target, lock['linuxSourceBuild']['compilerImage'])
        shutil.rmtree(target)
    print(json.dumps({'host': os.uname().nodename, 'cleanupSourceCommit': args.runner_source_commit,
        'ownedFailedSourceCommit': args.owned_source_commit, 'ownedJob': str(target),
        'pathGuardsRefused': refused,
        'deleted': not target.exists(), 'scope': 'Exact failed hardware scratch only; no neighbor/auth/state/host configuration changes.'}, indent=2))
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
