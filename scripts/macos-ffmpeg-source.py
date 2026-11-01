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


def verify_download(recipe, url, archive):
    lock = recipe / 'portico-macos-extras.lock.json'
    authority = json.loads((recipe / 'portico-ffmpeg-sources.lock.json').read_text())
    if lock.is_symlink() or digest(lock) != authority['macosRecipe']['extrasLock']['sha256']:
        raise ValueError('Mac archive pin lock differs from the sealed source contract')
    if archive.is_symlink() or not archive.is_file() or not archive.resolve().is_relative_to((recipe / 'packages').resolve()):
        raise ValueError('regular owned download required before extraction')
    expected = json.loads(lock.read_text()).get('archivePins', {}).get(url)
    # Transitional working builds only: the full Homebridge archive table is
    # a release hold, not permission to qualify unpinned legacy downloads.
    if expected is None:
        return None
    if not isinstance(expected, str) or not re.fullmatch(r'[a-f0-9]{64}', expected) or digest(archive) != expected:
        raise ValueError('Mac source archive checksum mismatch: ' + url)
    return expected


def download_retries(text):
    start = text.index('    curl -L --silent -o "${DOWNLOAD_PATH}/${DOWNLOAD_FILE}" "$1"')
    end = text.index('    echo "... Done"', start)
    original = text[start:end]
    if original.count('curl -L --silent -o') != 2 or 'Retrying in 10 seconds' not in original:
        raise ValueError('pinned Homebridge download retry boundary changed')
    replacement = '''    local portico_attempt
    local portico_backoff=(5 10 15 20)
    for portico_attempt in 1 2 3 4 5; do
      if curl -L --fail --silent --show-error --connect-timeout 10 --max-time 15  -o "${DOWNLOAD_PATH}/${DOWNLOAD_FILE}.portico-part" "$1"; then
        mv "${DOWNLOAD_PATH}/${DOWNLOAD_FILE}.portico-part" "${DOWNLOAD_PATH}/${DOWNLOAD_FILE}"
        break
      else
        EXITCODE=$?
      fi
      if [ "$portico_attempt" -eq 5 ]; then
        echo "Failed to download $1 after 5 attempts. Exitcode $EXITCODE" >&2
        exit 1
      fi
      sleep "${portico_backoff[$((portico_attempt - 1))]}"
    done

'''
    text = text[:start] + replacement + text[end:]
    marker = '  make_dir "${DOWNLOAD_PATH}/${TARGETDIR}"'
    if text.count(marker) != 1:
        raise ValueError('pinned Homebridge pre-extraction boundary changed')
    return text.replace(marker, '''  python3 "${CWD}/portico-macos-ffmpeg-source.py" verify-download --recipe "$CWD"  --url "$1" --archive "${DOWNLOAD_PATH}/${DOWNLOAD_FILE}" || exit "$?"
''' + marker, 1)


def configure(recipe):
    path = recipe / 'build-ffmpeg'
    text = download_retries(path.read_text())
    original = 'download "https://ffmpeg.org/releases/ffmpeg-${FFMPEG_VERSION}.tar.xz" "ffmpeg-${FFMPEG_VERSION}.tar.xz"'
    replacement = '''portico_source_archive=$(python3 "${CWD}/portico-macos-ffmpeg-source.py" verify --recipe "$CWD") || exit "$?"
download "file://${PACKAGES}/${portico_source_archive}" "$portico_source_archive" ""
python3 "${CWD}/portico-macos-ffmpeg-source.py" check-source --recipe "$CWD" --source "$PWD" || exit "$?"
'''
    if text.count(original) != 1:
        raise ValueError('pinned Homebridge final source boundary changed')
    path.write_text(text.replace(original, replacement.rstrip(), 1))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('mode', choices=('seed', 'verify', 'verify-download', 'check-source'))
    parser.add_argument('--recipe', type=Path, required=True)
    parser.add_argument('--source', type=Path)
    parser.add_argument('--url')
    parser.add_argument('--archive', type=Path)
    args = parser.parse_args()
    if args.mode == 'check-source':
        if args.source is None:
            parser.error('check-source requires source directory')
        collisions = sorted(p.name for p in (args.source / 'libavfilter').glob('*.m') if p.with_suffix('.c').exists())
        if collisions:
            raise SystemExit('C source shadows Objective-C filter: ' + ', '.join(collisions))
    elif args.mode == 'seed':
        if args.source is None:
            parser.error('prepared source archive required')
        print(seed(args.recipe, args.source))
    elif args.mode == 'verify-download':
        if args.url is None or args.archive is None:
            parser.error('verify-download requires URL and archive')
        print(json.dumps({'verifiedSHA256': verify_download(args.recipe, args.url, args.archive), 'release': False}))
    else:
        print(verify(args.recipe))
