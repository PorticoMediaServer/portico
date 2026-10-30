#!/usr/bin/env python3
"""Verify and apply the canonical Jellyfin patch series for every consumer."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import urllib.request

ROOT = Path(__file__).resolve().parent.parent


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def prepare(archive, output, lock):
    source = lock['ffmpeg']
    if digest(archive) != source['sha256']:
        raise ValueError('Jellyfin FFmpeg source checksum mismatch')
    if output.exists():
        raise ValueError('source output must not already exist')
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='.ffmpeg-source-', dir=output.parent) as tmp:
        tmp = Path(tmp)
        with tarfile.open(archive) as tar:
            tar.extractall(tmp, filter='data')
        roots = list(tmp.iterdir())
        if len(roots) != 1 or not roots[0].is_dir():
            raise ValueError('expected one source root')
        tree = roots[0]
        if digest(tree / source['patchSeries']) != source['patchSeriesSha256']:
            raise ValueError('Jellyfin patch series checksum mismatch')
        series = [line.split()[0] for line in (tree / source['patchSeries']).read_text().splitlines()
                  if line.strip() and not line.startswith('#')]
        if series != [Path(p['path']).name for p in source['patches']]:
            raise ValueError('Jellyfin patch order differs from lock')
        for patch in source['patches']:
            path = tree / patch['path']
            if digest(path) != patch['sha256']:
                raise ValueError('patch checksum mismatch: ' + patch['path'])
            subprocess.run(['patch', '--batch', '--fuzz=0', '-p1', '-i', str(path.resolve())],
                           cwd=tree, check=True, stdout=subprocess.DEVNULL)
        # These boundaries predate this release and must survive all patches.
        assertions = {
            'RELEASE': ['8.1.3'],
            'libavcodec/bsf/dovi_rpu.c': ['ff_dovi_rpu_bsf'],
            'libavformat/movenc.c': ['info->complexity_index_type_a = hdr->complexity_index_type_a',
                                     'flag_eac3_extension_type_a'],
            'libavfilter/vf_tonemap_cuda.c': ['dovi'],
            'libavfilter/vf_tonemap_opencl.c': ['dovi'],
            'libavfilter/vf_tonemap_videotoolbox.m': ['DOVI'],
        }
        for name, needles in assertions.items():
            data = (tree / name).read_text()
            for needle in needles:
                if needle.lower() not in data.lower():
                    raise ValueError('required source compatibility missing: ' + name + ': ' + needle)
        shutil.move(tree, output)
    # A portable patch receipt, useful for Android/desktop consumers, without
    # treating source inspection as target compilation or runtime acceptance.
    receipt = {'sourceCommit': source['commit'], 'sourceSha256': source['sha256'],
               'patches': source['patches'], 'sourceDateEpoch': lock['sourceDateEpoch']}
    (output / 'portico-source-receipt.json').write_text(json.dumps(receipt, indent=2) + '\n')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('output', type=Path)
    parser.add_argument('--archive', type=Path)
    parser.add_argument('--lock', type=Path, default=ROOT / 'third_party/ffmpeg/sources.lock.json')
    args = parser.parse_args()
    lock = json.loads(args.lock.read_text())
    if args.archive:
        prepare(args.archive, args.output, lock)
    else:
        with tempfile.TemporaryDirectory(prefix='portico-ffmpeg-download-') as tmp:
            archive = Path(tmp) / 'source.tar.gz'
            urllib.request.urlretrieve(lock['ffmpeg']['source'], archive)
            prepare(archive, args.output, lock)
    print('Verified source and applied %d patches: %s' % (len(lock['ffmpeg']['patches']), args.output))


if __name__ == '__main__':
    main()
