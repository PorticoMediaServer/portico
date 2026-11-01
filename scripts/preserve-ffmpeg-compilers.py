#!/usr/bin/env python3
"""Inspect locked upstream compiler images and preserve exact OCI digests."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile

ROOT = Path(__file__).resolve().parents[1]


def digest(data):
    return 'sha256:' + hashlib.sha256(data).hexdigest()


def lock(root=ROOT):
    source = json.loads((root / 'third_party/ffmpeg/sources.lock.json').read_text())['compilerImages']
    path = root / 'third_party/ffmpeg/compiler-images.lock.json'
    if source['path'] != path.relative_to(root).as_posix() or path.is_symlink() or digest(path.read_bytes()) != 'sha256:' + source['sha256']:
        raise ValueError('compiler image lock changed')
    data = json.loads(path.read_text())
    if (data.get('schemaVersion') != 1 or not re.fullmatch('[a-f0-9]{40}', data['upstreamCommit']) or
            data['upstreamRepository'] != 'https://github.com/BtbN/FFmpeg-Builds' or
            set(data['images']) != {'linux64', 'linuxarm64', 'win64'}):
        raise ValueError('invalid compiler pin set')
    for name, row in data['images'].items():
        if (not re.fullmatch(r'sha256:[a-f0-9]{64}', row['digest']) or
                row['upstream'] != 'ghcr.io/btbn/ffmpeg-builds/base-' + name + '@' + row['digest'] or
                row['mirror'] != 'ghcr.io/porticomediaserver/ffmpeg-base-' + name + '@' + row['digest']):
            raise ValueError('compiler source/mirror digest or repository mismatch')
    return data


def raw(image):
    return subprocess.check_output(['docker', 'buildx', 'imagetools', 'inspect', image, '--raw'])


def verified_manifest(row, image=None, reader=None):
    reader = raw if reader is None else reader
    value = reader(image or row['upstream'])
    if digest(value) != row['digest'] or json.loads(value).get('manifests') != row['manifests']:
        raise ValueError('compiler OCI index bytes/platforms changed')
    for child in row['manifests']:
        reference = (image or row['upstream']).split('@')[0] + '@' + child['digest']
        data = reader(reference)
        if digest(data) != child['digest'] or len(data) != child['size']:
            raise ValueError('compiler child manifest changed')
    return value


def inside():
    """Actual installed metadata only; no compiler or runtime acceptance."""
    import base64
    import shlex
    target = os.environ['FFBUILD_TOOLCHAIN']
    paths = [Path('/opt/ct-ng/share') / (target + '-ct-ng.config.bz2'),
             Path('/opt/implib/LICENSE.txt'), Path('/usr/share/doc/gcc-16-base/copyright'),
             Path('/usr/share/common-licenses/GPL-3')]
    files = {str(p): {'sha256': hashlib.sha256(p.read_bytes()).hexdigest(),
                      'base64': base64.b64encode(p.read_bytes()).decode()}
             for p in paths if p.is_file()}
    versions = {key: subprocess.check_output(shlex.split(os.environ[key]) + ['--version'], text=True).splitlines()[0]
                for key in ('CC', 'CXX', 'LD', 'AR')}
    implib = Path('/opt/implib/.git')
    print(json.dumps({'target': target, 'files': files, 'tools': versions,
        'implibCommit': subprocess.check_output(['git', '-C', '/opt/implib', 'rev-parse', 'HEAD'], text=True).strip() if implib.exists() else None,
        'scope': 'Read-only installed compiler metadata; not source custody or engine qualification.'}))


def inspect(name, work):
    row = lock()['images'][name]
    work.mkdir(parents=True, exist_ok=False)
    (work / 'upstream-index.json').write_bytes(verified_manifest(row))
    subprocess.run(['docker', 'pull', row['upstream']], check=True)
    value = subprocess.check_output(['docker', 'run', '--rm', '--network', 'none', '--cpus', '1', '--memory', '512m',
        '--mount', 'type=bind,src=' + str(Path(__file__).resolve()) + ',dst=/inspect.py,readonly',
        row['upstream'], 'python3', '/inspect.py', 'inside'])
    json.loads(value)
    (work / 'installed-metadata.json').write_bytes(value)


def preserve(name, work):
    env = os.environ
    if (env.get('GITHUB_ACTIONS') != 'true' or env.get('RUNNER_ENVIRONMENT') != 'github-hosted' or
            env.get('GITHUB_REPOSITORY') != 'PorticoMediaServer/portico' or
            env.get('GITHUB_EVENT_NAME') != 'workflow_dispatch' or env.get('GITHUB_REF') != 'refs/heads/main' or
            env.get('GITHUB_WORKFLOW_REF') != 'PorticoMediaServer/portico/.github/workflows/ffmpeg-compiler-custody.yml@refs/heads/main'):
        raise ValueError('compiler preservation requires the main-only manual hosted job')
    pins = lock(); row = pins['images'][name]
    work.mkdir(parents=True, exist_ok=False)
    original = verified_manifest(row)
    (work / 'upstream-index.json').write_bytes(original)
    tag = row['mirror'].split('@')[0] + ':sha256-' + row['digest'].split(':')[1]
    existing = subprocess.run(['skopeo', 'inspect', '--raw', 'docker://' + tag], capture_output=True)
    if existing.returncode == 0 and digest(existing.stdout) != row['digest']:
        raise ValueError('refusing to replace an existing compiler tag with other bytes')
    if existing.returncode and not any(term in existing.stderr.lower() for term in (b'manifest unknown', b'name unknown')):
        raise ValueError('cannot prove compiler tag absent; refusing an overwrite: ' + existing.stderr.decode(errors='replace')[-500:])
    receipt = {'schemaVersion': 1, 'upstreamCommit': pins['upstreamCommit'], **row,
               'engineQualified': False, 'registryCopyVerified': False,
               'copyUtility': subprocess.check_output(['skopeo', '--version'], text=True).strip()}
    try:
        subprocess.run(['skopeo', 'copy', '--all', '--preserve-digests', '--digestfile', str(work / 'copied.digest'),
                        'docker://' + row['upstream'], 'docker://' + tag], check=True)
        if (work / 'copied.digest').read_text().strip() != row['digest']:
            raise ValueError('compiler copy digest changed')
        def authenticated_raw(image):
            return subprocess.check_output(['skopeo', 'inspect', '--raw', 'docker://' + image])
        if verified_manifest(row, row['mirror'], authenticated_raw) != original:
            raise ValueError('preserved compiler bytes differ')
        receipt['registryCopyVerified'] = True
    except Exception as error:
        receipt['registryCopyFailure'] = str(error)
        layout = work / 'oci'
        subprocess.run(['skopeo', 'copy', '--all', '--preserve-digests', 'docker://' + row['upstream'],
                        'oci:' + str(layout) + ':compiler'], check=True)
        expected = layout / 'blobs/sha256' / row['digest'].split(':')[1]
        if not expected.is_file() or expected.read_bytes() != original:
            raise ValueError('OCI fallback does not retain the exact original compiler index')
        for blob in (layout / 'blobs/sha256').iterdir():
            with blob.open('rb') as stream:
                if hashlib.file_digest(stream, 'sha256').hexdigest() != blob.name:
                    raise ValueError('OCI fallback blob changed')
        archive = work / ('ffmpeg-base-' + name + '.oci.tar')
        with tarfile.open(archive, 'w') as output:
            output.add(layout, arcname='oci')
        with archive.open('rb') as stream:
            receipt['ociArchiveSHA256'] = hashlib.file_digest(stream, 'sha256').hexdigest()
        receipt['ociFallbackRequiresEngineReleaseAttachmentAndReviewedPin'] = True
        shutil.rmtree(layout)
    finally:
        (work / 'preservation.json').write_text(json.dumps(receipt, indent=2) + '\n')
    if not receipt['registryCopyVerified']:
        raise ValueError('registry mirror not accepted; exact OCI fallback retained for the engine release')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('mode', choices=('inspect', 'preserve', 'inside'))
    parser.add_argument('--target', choices=('linux64', 'linuxarm64', 'win64'))
    parser.add_argument('--work', type=Path)
    args = parser.parse_args()
    if args.mode == 'inside':
        inside()
    elif args.target is None or args.work is None:
        parser.error('target and fresh work directory required')
    elif args.mode == 'inspect':
        inspect(args.target, args.work.absolute())
    else:
        preserve(args.target, args.work.absolute())
