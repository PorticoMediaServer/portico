#!/usr/bin/env python3
"""Bind Homebridge's final download to the already checked patched source."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import shutil


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def seed(recipe, source):
    if source.is_symlink() or not source.is_file():
        raise ValueError('regular prepared source archive required')
    sha = digest(source)
    name = 'portico-ffmpeg-source-' + sha + '.tar.xz'
    destination = recipe / 'packages' / name
    destination.parent.mkdir(exist_ok=True)
    if destination.exists() or destination.is_symlink():
        raise ValueError('fresh prepared source input required')
    shutil.copyfile(source, destination)
    if digest(source) != sha or digest(destination) != sha:
        raise ValueError('prepared source changed during copy')
    destination.chmod(0o444)
    record = dict(schemaVersion=1, filename=name, preparedSourceArchiveSHA256=sha,
                  sourcesLockSHA256=digest(recipe / 'portico-ffmpeg-sources.lock.json'), release=False)
    path = recipe / 'portico-ffmpeg-source.json'
    temporary = path.with_suffix('.new')
    temporary.write_text(json.dumps(record, indent=2) + '\n')
    temporary.replace(path)
    return verify(recipe)


def verify(recipe):
    receipt = recipe / 'portico-ffmpeg-source.json'
    if receipt.is_symlink() or not receipt.is_file() or receipt.stat().st_size > 4096:
        raise ValueError('prepared source receipt missing, linked or oversized')
    record = json.loads(receipt.read_text())
    sha = record.get('preparedSourceArchiveSHA256')
    if (record.get('schemaVersion') != 1 or record.get('release') is not False or
            not isinstance(sha, str) or not re.fullmatch(r'[a-f0-9]{64}', sha) or
            record.get('filename') != 'portico-ffmpeg-source-' + sha + '.tar.xz' or
            record.get('sourcesLockSHA256') != digest(recipe / 'portico-ffmpeg-sources.lock.json')):
        raise ValueError('prepared source receipt/contract mismatch')
    path = recipe / 'packages' / record['filename']
    if path.is_symlink() or not path.is_file() or digest(path) != sha:
        raise ValueError('prepared source archive missing or changed; no upstream fallback')
    return record['filename']


def extras_block():
    # An unconditionally executed subshell preserves errexit inside sourced
    # helpers. An `||` around it would suppress Bash's errexit in the helpers.
    return '''(
  source "${CWD}/portico-macos-extras.sh"
  if declare -F portico_time_end >/dev/null; then portico_time_end; fi
)
portico_extras_status=$?
if [ "$portico_extras_status" -ne 0 ]; then exit "$portico_extras_status"; fi
'''


def configure(recipe):
    path = recipe / 'build-ffmpeg'
    text = path.read_text()
    original = 'download "https://ffmpeg.org/releases/ffmpeg-${FFMPEG_VERSION}.tar.xz" "ffmpeg-${FFMPEG_VERSION}.tar.xz"'
    replacement = '''portico_source_archive=$(python3 "${CWD}/portico-macos-ffmpeg-source.py" verify --recipe "$CWD") || exit "$?"
download "file://${PACKAGES}/${portico_source_archive}" "$portico_source_archive" ""
'''
    if text.count(original) != 1:
        raise ValueError('pinned Homebridge final source boundary changed')
    path.write_text(text.replace(original, replacement.rstrip(), 1))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('mode', choices=('seed', 'verify'))
    parser.add_argument('--recipe', type=Path, required=True)
    parser.add_argument('--source', type=Path)
    args = parser.parse_args()
    if args.mode == 'seed':
        if args.source is None:
            parser.error('prepared source archive required')
        print(seed(args.recipe, args.source))
    else:
        print(verify(args.recipe))
