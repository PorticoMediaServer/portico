#!/usr/bin/env python3
"""Run each target's bundled disc probe against checked, hash-pinned fixtures."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import threading

REQUIRED = {'bluray-two-clip-bdmv', 'dvd-two-title-video-ts', 'bluray-udf250-iso'}
LIMIT = 1 << 20


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def fixture_path(directory, entry):
    path = directory / entry['path']
    if path.is_symlink() or not path.resolve().is_relative_to(directory.resolve()):
        raise ValueError('fixture must remain inside its checked fixture directory')
    if path.is_file():
        actual = {'.': digest(path)}
    elif path.is_dir():
        actual = {}
        for file in path.rglob('*'):
            if file.is_symlink():
                raise ValueError('symlink in disc fixture')
            if file.is_file():
                actual[file.relative_to(path).as_posix()] = digest(file)
    else:
        raise ValueError('disc fixture missing: ' + str(path))
    if not actual or actual != entry['files']:
        raise ValueError('disc fixture inventory/hash changed: ' + entry['id'])
    return path.resolve()


def run_helper(tool, action, path, *arguments):
    env = {'PATH': os.defpath, 'LC_ALL': 'C'}
    if os.name == 'nt':
        env['SystemRoot'] = os.environ['SystemRoot']
    with subprocess.Popen([str(tool), action, str(path), *arguments], stdin=subprocess.DEVNULL,
                          stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, env=env,
                          start_new_session=os.name != 'nt') as process:
        raw = bytearray()
        overflow = False
        read_errors = []

        def kill():
            try:
                if os.name == 'nt':
                    process.kill()
                else:
                    os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass

        def read():
            nonlocal overflow
            try:
                while block := process.stdout.read(min(65536, LIMIT + 1 - len(raw))):
                    raw.extend(block)
                    if len(raw) > LIMIT:
                        overflow = True
                        kill()
                        return
            except OSError as error:
                read_errors.append(error)

        reader = threading.Thread(target=read, daemon=True)
        reader.start()
        try:
            code = process.wait(timeout=20)
        except subprocess.TimeoutExpired:
            kill()
            process.wait()
            raise ValueError('disc probe exceeded 20 seconds')
        finally:
            reader.join(timeout=1)
            kill()
            reader.join(timeout=1)
        if reader.is_alive() or read_errors or overflow or code:
            raise ValueError('disc probe failed or exceeded its 1 MiB output limit')
    return json.loads(raw)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('bundle', type=Path)
    parser.add_argument('--fixtures', required=True, type=Path)
    parser.add_argument('--fixture-sha256', required=True)
    parser.add_argument('--supplemental-fixtures', type=Path)
    parser.add_argument('--report', type=Path)
    args = parser.parse_args()
    manifest = json.loads((args.bundle / 'toolchain-manifest.json').read_text())
    target = manifest['target']
    tool = args.bundle.resolve() / 'bin' / ('portico-disc.exe' if target.startswith('windows-') else 'portico-disc')
    metadata = json.loads((args.bundle / 'portico-disc-build.json').read_text())
    source_lock = json.loads((args.bundle / 'sources.lock.json').read_text())['discHelper']
    if (metadata.get('schema') != 1 or metadata.get('tool') != 'portico-disc' or
            metadata.get('target') != target or metadata.get('decryption') is not False or
            metadata.get('binarySha256') != digest(tool) or
            not re.fullmatch(r'[0-9a-f]{40}', metadata.get('sourceCommit', '')) or
            not re.fullmatch(r'[0-9a-f]{64}', metadata.get('sourceSha256', ''))):
        raise ValueError('disc build identity does not match the bundle')
    if (metadata.get('sourceCommit') != source_lock['sourceCommit'] or
            metadata.get('sourceFiles') != source_lock['sourceFiles']):
        raise ValueError('disc source C/header cohort differs from its canonical source lock')
    libraries = metadata.get('staticLibraries', {})
    if set(libraries) != {'libbluray', 'libudfread', 'dvdnav', 'dvdread'}:
        raise ValueError('all four static disc libraries are mandatory')
    for library in libraries.values():
        archive = Path(library['archive'])
        if (archive.is_absolute() or '..' in archive.parts or archive.suffix != '.a' or
                not re.fullmatch(r'[0-9a-f]{64}', library['sha256'])):
            raise ValueError('invalid static disc library provenance')
    if digest(args.fixtures) != args.fixture_sha256:
        raise ValueError('disc fixture manifest changed from the checked source handoff')
    if args.fixture_sha256 != source_lock['fixtureManifestSHA256']:
        raise ValueError('disc fixture manifest differs from canonical source lock')
    contract = json.loads(args.fixtures.read_text())
    fixtures = contract['fixtures']
    if (contract.get('schema') != 1 or len(fixtures) != len(REQUIRED) or
            {entry['id'] for entry in fixtures} != REQUIRED):
        raise ValueError('the two-clip BDMV, two-title DVD and UDF 2.50 ISO fixtures are required')
    results = []
    checked_paths = {}
    for entry in fixtures:
        path = fixture_path(args.fixtures.parent, entry)
        checked_paths[entry['id']] = path
        result = run_helper(tool, 'probe', path)
        # Exact parsed JSON comes from the video-owned checked schema/fixture
        # producer. Do not invent field names, title facts or permissive subsets.
        if not isinstance(entry['expected'], dict) or result != entry['expected']:
            raise ValueError('disc structure differs from checked fixture: ' + entry['id'])
        row = {'id': entry['id'], 'probe': result}
        if entry['id'] in {'bluray-two-clip-bdmv', 'bluray-udf250-iso'}:
            # These exact fixture bytes and the observed 37s clip boundary are
            # the MAIN video component's authority, not assumed recipe times.
            seek = run_helper(tool, 'seek', path, '1', '37500')
            expected_seek = {'schema': 1, 'playlist': 1, 'requestedMs': 37500, 'titleTimeMs': 37000}
            offset = seek.get('byteOffset')
            if (set(seek) != set(expected_seek) | {'byteOffset'} or
                    any(seek.get(key) != value for key, value in expected_seek.items()) or
                    type(offset) is not int or not 0 <= offset < 1 << 63):
                raise ValueError('target disc helper cross-clip seek differs from checked fixture: ' + entry['id'])
            row['seek'] = seek
        results.append(row)
    supplemental_path = args.supplemental_fixtures or args.fixtures.parent / 'dovi/manifest.json'
    if digest(supplemental_path) != source_lock['supplementalFixtureManifestSHA256']:
        raise ValueError('supplemental STN/PMT disc fixture manifest changed')
    supplemental = json.loads(supplemental_path.read_text())
    if (supplemental.get('schema') != 1 or len(supplemental['fixtures']) != 2 or
            {entry['id'] for entry in supplemental['fixtures']} != {'stn-fel-bdmv', 'pmt-fel-bdmv'}):
        raise ValueError('both genuine STN-only and explicit-PMT disc cases are required')
    for entry in supplemental['fixtures']:
        path = fixture_path(supplemental_path.parent, entry)
        result = run_helper(tool, 'probe', path)
        if result != entry['expected']:
            raise ValueError('disc STN/PMT structure differs from checked source: ' + entry['id'])
        results.append({'id': entry['id'], 'probe': result})
    if digest(tool) != metadata['binarySha256']:
        raise ValueError('Disc helper changed during fixture qualification')
    report = {'target': target, 'toolSha256': digest(tool),
              'fixtureManifestSha256': digest(args.fixtures),
              'supplementalFixtureManifestSha256': digest(supplemental_path), 'fixtures': results}
    spec = importlib.util.spec_from_file_location('bluray_protocol_verifier', Path(__file__).with_name('verify-ffmpeg-bluray.py'))
    protocol = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(protocol)
    ffmpeg = args.bundle.resolve() / 'bin' / ('ffmpeg.exe' if target.startswith('windows-') else 'ffmpeg')
    report['blurayProtocol'] = protocol.qualify(ffmpeg, checked_paths)
    for entry in fixtures:
        fixture_path(args.fixtures.parent, entry)
    if args.report:
        args.report.write_text(json.dumps(report, indent=2, sort_keys=True) + '\n')
    print('Verified disc fixtures for ' + target)


if __name__ == '__main__':
    main()
