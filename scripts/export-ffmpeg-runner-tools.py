#!/usr/bin/env python3
"""Export the three tools from an already qualified, hash-bound Linux bundle.

Launch through the published runner only after the build source is on MAIN.
The mini-1 launcher checks that ancestry against origin/main before transfer.
This copies our source-built product; it never downloads or executes media tools.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import tarfile

TOOLS = ('ffmpeg', 'ffprobe', 'portico-disc')
ARCHIVE = 'portico-ffmpeg-linux-x64.tar.xz'


def open_file(root, parts, limit):
    directory = os.open(root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    try:
        for name in parts[:-1]:
            child = os.open(name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=directory)
            os.close(directory)
            directory = child
        descriptor = os.open(parts[-1], os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=directory)
    finally:
        os.close(directory)
    stream = os.fdopen(descriptor, 'rb')
    info = os.fstat(stream.fileno())
    if not stat.S_ISREG(info.st_mode) or not 0 < info.st_size <= limit:
        stream.close()
        raise ValueError('Retained artifact is not a bounded regular file')
    return stream


def checked_json(root, parts, expected):
    with open_file(root, parts, 2 << 20) as stream:
        raw = stream.read((2 << 20) + 1)
    if len(raw) > 2 << 20 or hashlib.sha256(raw).hexdigest() != expected:
        raise ValueError('Retained text receipt hash changed: ' + parts[-1])
    return json.loads(raw)


def export_tools(artifact_root, output, build_commit, build_job, expected):
    if (not re.fullmatch(r'[0-9a-f]{40}', build_commit) or
            not re.fullmatch(re.escape(build_commit[:12]) + r'-[0-9a-f]{8}', build_job) or
            set(expected) != {'archive', 'manifest', 'receipt'} or
            any(not re.fullmatch(r'[0-9a-f]{64}', value) for value in expected.values())):
        raise ValueError('Exact build source, job and three SHA256 identities are required')
    cohort = (build_commit, build_job)
    receipt = checked_json(artifact_root, (*cohort, 'artifact-receipt.json'), expected['receipt'])
    manifest = checked_json(artifact_root, (*cohort, 'toolchain-manifest.json'), expected['manifest'])
    if (receipt.get('schema') != 1 or receipt.get('buildSourceCommit') != build_commit or
            receipt.get('qualifiedTargetVerification') is not True or
            receipt.get('artifactPath') != str(artifact_root / build_commit / build_job / ARCHIVE) or
            receipt.get('files', {}).get(ARCHIVE) != expected['archive'] or
            receipt['files'].get('toolchain-manifest.json') != expected['manifest'] or
            manifest.get('schemaVersion') != 1 or manifest.get('licenseMode') != 'gpl3' or
            manifest.get('target') != 'linux-x64'):
        raise ValueError('Retained bundle is not this qualified Linux source-build cohort')
    files = manifest.get('files', {})
    required = ('corresponding-source.tar.xz', 'sources.lock.json', 'NOTICE.md', 'portico-disc-build.json')
    if not all(name in files for name in required):
        raise ValueError('Retained bundle lacks required source and helper provenance')
    hashes = {name: files.get('bin/' + name) for name in TOOLS}
    if any(not isinstance(value, str) or not re.fullmatch(r'[0-9a-f]{64}', value)
           for value in hashes.values()):
        raise ValueError('Retained manifest lacks exact three-tool identities')
    result = {}
    with open_file(artifact_root, (*cohort, ARCHIVE), 8 << 30) as archive:
        if hashlib.file_digest(archive, 'sha256').hexdigest() != expected['archive']:
            raise ValueError('Retained bundle archive hash changed')
        archive.seek(0)
        with tarfile.open(fileobj=archive, mode='r|xz') as bundle:
            members, unpacked = 0, 0
            for member in bundle:
                members += 1
                unpacked += member.size
                if members > 8192 or unpacked > 16 << 30:
                    raise ValueError('Retained bundle exceeded its inventory or stream bound')
                path = member.name.removeprefix('./')
                if path not in {'bin/' + name for name in TOOLS}:
                    continue
                name = path.removeprefix('bin/')
                if name in result or not member.isfile() or not member.mode & 0o111 or not 64 <= member.size <= 512 << 20:
                    raise ValueError('Duplicate, linked, non-executable or oversized tool member')
                temporary = output / (name + '.new-' + str(os.getpid()))
                try:
                    digest, size, header = hashlib.sha256(), 0, b''
                    with bundle.extractfile(member) as source, temporary.open('xb') as destination:
                        while chunk := source.read(1 << 20):
                            size += len(chunk)
                            if size > member.size:
                                raise ValueError('Tool member exceeded declared size')
                            if not header:
                                header = chunk[:64]
                            destination.write(chunk)
                            digest.update(chunk)
                    if (size != member.size or digest.hexdigest() != hashes[name] or
                            header[:7] != b'\x7fELF\x02\x01\x01' or header[18:20] != b'\x3e\x00' or
                            int.from_bytes(header[16:18], 'little') not in (2, 3)):
                        raise ValueError('Tool bytes, architecture or manifest identity changed: ' + name)
                    temporary.chmod(0o755)
                    # Atomic and deliberately refuses an existing destination.
                    os.link(temporary, output / name, follow_symlinks=False)
                    result[name] = {'bytes': size, 'sha256': hashes[name]}
                finally:
                    temporary.unlink(missing_ok=True)
        archive.seek(0)
        if hashlib.file_digest(archive, 'sha256').hexdigest() != expected['archive']:
            raise ValueError('Retained bundle changed during export')
    if set(result) != set(TOOLS):
        raise ValueError('Retained bundle is missing a required executable')
    return {'schema': 1, 'buildSourceCommit': build_commit, 'buildJob': build_job,
            'qualifiedBundleHashes': expected, 'tools': result,
            'originalBuildReceipt': receipt, 'bundleManifest': manifest,
            'scope': 'Custody of the original source-built tools only; no new build, hardware, playback or platform acceptance.'}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--collector-source-commit', required=True)
    parser.add_argument('--main-commit', required=True)
    parser.add_argument('--build-source-commit', required=True)
    parser.add_argument('--build-job', required=True)
    parser.add_argument('--bundle-sha256', required=True)
    parser.add_argument('--manifest-sha256', required=True)
    parser.add_argument('--receipt-sha256', required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    if (root.name != 'source' or root.parent.parent.name != 'hardware-r29' or
            root.parents[2].name != 'jobs' or
            not re.fullmatch(r'[0-9a-f]{40}', args.collector_source_commit) or
            not re.fullmatch(re.escape(args.collector_source_commit[:12]) + r'-[0-9a-f]{8}', root.parent.name) or
            not re.fullmatch(r'[0-9a-f]{40}', args.main_commit)):
        raise ValueError('Use an exact published hardware runner source and caller-verified MAIN commit')
    output = Path(os.environ['PORTICO_RUNNER_OUTPUT'])
    if output.is_symlink() or output.resolve(strict=True) != root.parent / 'outputs':
        raise ValueError('Export must use this runner output directory')
    record = {'collectorSourceCommit': args.collector_source_commit,
              'mainCommitProvidedByLauncher': args.main_commit, 'exported': False}
    try:
        record.update(export_tools(root.parents[3] / 'artifacts/hardware-r29', output,
            args.build_source_commit, args.build_job, {'archive': args.bundle_sha256,
                'manifest': args.manifest_sha256, 'receipt': args.receipt_sha256}))
        record['exported'] = True
    except BaseException as error:
        record['failure'] = (type(error).__name__ + ': ' + str(error))[:4000]
        raise
    finally:
        path = output / ('export-receipt.log.new-' + str(os.getpid()))
        try:
            with path.open('x') as stream:
                json.dump(record, stream, indent=2)
                stream.write('\n')
            path.chmod(0o644)
            path.replace(output / 'export-receipt.log')
        finally:
            path.unlink(missing_ok=True)
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
