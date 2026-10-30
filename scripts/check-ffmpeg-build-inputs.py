#!/usr/bin/env python3
"""Verify checked MAIN inputs in a Git checkout or an explicit runner archive."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def verify(root):
    lock = json.loads((root / 'third_party/ffmpeg/sources.lock.json').read_text())
    entry = lock['sourceAuthority']
    authority_file = (root / entry['path']).resolve(strict=True)
    if not authority_file.is_relative_to(root) or digest(authority_file) != entry['sha256']:
        raise ValueError('Checked source authority changed')
    authority = json.loads(authority_file.read_text())
    for name, expected in authority['files'].items():
        path = root / name
        if (path.is_symlink() or not path.is_file() or not path.resolve().is_relative_to(root) or
                digest(path) != expected):
            raise ValueError('Checked MAIN source/fixture input changed: ' + name)
    if (root / '.git').exists():
        for revision in ('HEAD', 'origin/main'):
            subprocess.run(['git', '-C', str(root), 'merge-base', '--is-ancestor',
                            authority['checkedMain'], revision], check=True)
        subprocess.run(['git', '-C', str(root), 'diff', '--quiet', 'HEAD', '--', *authority['files']], check=True)
        commit = subprocess.check_output(['git', '-C', str(root), 'rev-parse', 'HEAD'], text=True).strip()
        kind = 'Git checkout with checked MAIN ancestry and unchanged source inputs'
    else:
        # The temporary remote runner already exports an exact pushed Git
        # commit and returns a host/commit receipt. Its caller must pass that
        # exact commit explicitly; absence of Git never silently admits input.
        commit = os.environ.get('PORTICO_RUNNER_SOURCE_COMMIT', '')
        if not re.fullmatch(r'[0-9a-f]{40}', commit):
            raise ValueError('Archive builds require the exact runner-attested PORTICO_RUNNER_SOURCE_COMMIT')
        if (root.name != 'source' or root.parent.parent.parent.name != 'jobs' or
                not re.fullmatch(re.escape(commit[:12]) + r'-[0-9a-f]{8}', root.parent.name)):
            raise ValueError('Archive source commit differs from the exact runner job directory')
        kind = 'Exact published Git archive; external runner host/commit receipt required'
    return {'schema': 1, 'kind': kind, 'buildSourceCommit': commit,
            'checkedMain': authority['checkedMain'], 'authoritySHA256': entry['sha256'],
            'verifiedInputFiles': authority['files'], 'scope': authority['scope']}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path)
    args = parser.parse_args()
    result = verify(Path(__file__).resolve().parents[1])
    raw = json.dumps(result, indent=2) + '\n'
    if args.output:
        args.output.write_text(raw)
    print(json.dumps({k: result[k] for k in ('schema', 'kind', 'buildSourceCommit', 'checkedMain', 'authoritySHA256')}, indent=2))


if __name__ == '__main__':
    main()
