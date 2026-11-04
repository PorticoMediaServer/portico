"""Provision checked Robolectric SDK jars before offline JVM tests start."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
REPOSITORY = 'https://repo.maven.apache.org/maven2/org/robolectric/android-all-instrumented'


def checked(path, digest):
    with path.open('rb') as source:
        actual = hashlib.file_digest(source, 'sha512').hexdigest()
    if actual != digest:
        raise ValueError(f'Robolectric SDK checksum mismatch: {path.name}')


def provision(directory, sdk):
    version, digest = sdk['version'], sdk['sha512']
    if not re.fullmatch(r'[A-Za-z0-9_.-]+', version) or not re.fullmatch(r'[a-f0-9]{128}', digest):
        raise ValueError('Invalid pinned Robolectric SDK')
    name = f'android-all-instrumented-{version}.jar'
    directory.mkdir(parents=True, exist_ok=True)
    target = directory / name
    if target.exists():
        checked(target, digest)  # A cache hit is never an integrity exemption.
        return target
    with tempfile.NamedTemporaryFile(dir=directory, prefix=name + '.', delete=False) as temporary:
        pending = Path(temporary.name)
    try:
        # Retry only dependency acquisition, before any test begins. A failed
        # download or checksum fails setup; no test is skipped or retried.
        subprocess.run([
            'curl', '--fail', '--location', '--silent', '--show-error',
            '--retry', '3', '--retry-max-time', '120', '--connect-timeout', '15',
            '--max-time', '120', '--output', str(pending), f'{REPOSITORY}/{version}/{name}',
        ], check=True)
        checked(pending, digest)
        pending.replace(target)
    finally:
        pending.unlink(missing_ok=True)
    return target


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--directory', required=True, type=Path)
    args = parser.parse_args()
    manifest = json.loads((ROOT / 'ci/robolectric-sdk.json').read_text())
    gradle = (ROOT / 'apps/native/android/app/build.gradle').read_text()
    if f'org.robolectric:robolectric:{manifest["robolectric"]}' not in gradle:
        raise ValueError('Update the pinned SDK manifest for the configured Robolectric version')
    for sdk in manifest['sdks']:
        print(f'Checked API {sdk["api"]}: {provision(args.directory, sdk).name}')


if __name__ == '__main__':
    main()
