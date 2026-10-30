#!/usr/bin/env python3
"""Reuse only unchanged compiler objects for the additive Blu-ray protocol patch.

The original SDK stays read-only. Source is freshly prepared from the locked
archive; no old source/build tree is copied. A clean build remains supported.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil

PAIR = {'ffmpeg': 'e45b768839cb9d169196f6b94845b334b7bfc439e6cf6b301efe769c5f3b5b2e',
        'ffprobe': 'b83289e34eda99fbc3950f2765819b915befbcc9e7fb8e4c352a06e977a2158d'}
GENERATED = ('config.h', 'config_components.h', 'config.asm', 'ffbuild/config.mak',
             'ffbuild/config.sh', 'libavutil/avconfig.h', 'libavutil/ffversion.h',
             'libavcodec/codec_list.c', 'libavcodec/parser_list.c', 'libavcodec/bsf_list.c',
             'libavformat/demuxer_list.c', 'libavformat/muxer_list.c', 'libavformat/protocol_list.c',
             'libavfilter/filter_list.c', 'libavdevice/indev_list.c', 'libavdevice/outdev_list.c')
def changed_sources(lock):
    paths = tuple(patch['path'] for patch in lock['ffmpeg']['postSeriesPatches'][2:])
    bluray = 'third_party/ffmpeg/portico-bluray-playlist-minimum.patch'
    hdr = 'third_party/ffmpeg/portico-x265-hdr10plus.patch'
    if paths not in ((bluray,), (bluray, hdr)):
        raise ValueError('Object reuse requires the exact qualified incremental patch sequence')
    return {'libavformat/bluray.c'} | ({'libavcodec/libx265.c'} if len(paths) == 2 else set())


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def objects(cache):
    return sorted(path for path in (cache / 'source').rglob('*')
                  if path.suffix in ('.o', '.a', '.d') and (path.is_file() or path.is_symlink()))


def fingerprint(cache, lock, pair=PAIR):
    cache = cache.resolve(strict=True)
    source = cache / 'source'
    receipt = json.loads((source / 'portico-source-receipt.json').read_text())
    patches = lock['ffmpeg']['postSeriesPatches']
    changed_sources(lock)
    if (
            receipt['sourceCommit'] != lock['ffmpeg']['commit'] or receipt['sourceSha256'] != lock['ffmpeg']['sha256'] or
            receipt['patches'] != lock['ffmpeg']['patches'] or receipt['postSeriesPatches'] != patches[:2]):
        raise ValueError('Object reuse is confined to the exact f3 source plus the additive Blu-ray patch')
    compiler = json.loads((cache / 'prefix/compiler-linked-inputs.json').read_text())
    if compiler['compilerImage'] != lock['linuxSourceBuild']['compilerImage']:
        raise ValueError('Object cache compiler differs from this target')
    for name, expected in pair.items():
        if digest(cache / 'prefix/bin' / name) != expected:
            raise ValueError('Object cache is not the retained f3 tool pair')
    for name, expected in receipt['files'].items():
        path = source / name
        if path.is_symlink():
            if expected != 'symlink:' + os.readlink(path):
                raise ValueError('Cached source link changed: ' + name)
        elif digest(path) != expected:
            raise ValueError('Cached source changed: ' + name)
    result, total = hashlib.sha256(), 0
    for path in [source / 'portico-source-receipt.json', *(source / name for name in GENERATED if (source / name).exists()), *objects(cache)]:
        if path.is_symlink() or not path.is_file() or not path.resolve().is_relative_to(source):
            raise ValueError('Object cache contains a non-regular input')
        size = path.stat().st_size
        total += size
        if size > 256 << 20 or total > 1 << 30:
            raise ValueError('Object cache exceeds its bounded compiler input')
        result.update(str(path.relative_to(source)).encode() + b'\0' + digest(path).encode() + b'\0')
    return result.hexdigest()


def reuse(cache, source, lock, expected, pair=PAIR):
    if fingerprint(cache, lock, pair) != expected:
        raise ValueError('Object cache changed before reuse')
    original = cache / 'source'
    old = json.loads((original / 'portico-source-receipt.json').read_text())
    new = json.loads((source / 'portico-source-receipt.json').read_text())
    if (new['postSeriesPatches'] != lock['ffmpeg']['postSeriesPatches'] or set(old['files']) != set(new['files']) or
            {name for name in old['files'] if old['files'][name] != new['files'][name]} != changed_sources(lock)):
        raise ValueError('Only the checked incremental C source files may differ')
    # Configure ran afresh. Its outputs must be identical before cache reuse;
    # timestamps then preserve normal make dependency ordering, not semantics.
    for name in GENERATED:
        previous, current = original / name, source / name
        if not previous.exists():
            continue
        if not current.exists() and name == 'libavutil/ffversion.h':
            shutil.copy2(previous, current)
        if not current.is_file() or digest(current) != digest(previous):
            raise ValueError('Fresh configure/generated inputs differ: ' + name)
        os.utime(current, ns=(previous.stat().st_atime_ns, previous.stat().st_mtime_ns))
    for name in old['files']:
        previous, current = original / name, source / name
        if name not in changed_sources(lock) and not current.is_symlink():
            os.utime(current, ns=(previous.stat().st_atime_ns, previous.stat().st_mtime_ns))
    changed = changed_sources(lock)
    skipped = {str(Path(name).with_suffix(suffix)) for name in changed for suffix in ('.o', '.d')}
    skipped |= {Path(name).parts[0] + '/' + Path(name).parts[0] + '.a' for name in changed}
    copied = 0
    for path in objects(cache):
        name = path.relative_to(original)
        if name.as_posix() in skipped:
            continue
        destination = source / name
        if not destination.resolve().is_relative_to(source.resolve()) or destination.exists():
            raise ValueError('Fresh output path already exists or escaped: ' + str(name))
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(path, destination)
        copied += 1
    if fingerprint(cache, lock, pair) != expected:
        raise ValueError('Read-only predecessor cache changed during reuse')
    print('Reused %d unchanged compiler objects/dependency files; rebuilding the checked changed C objects and relinking tools' % copied, flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--cache', type=Path, required=True)
    parser.add_argument('--source', type=Path, required=True)
    parser.add_argument('--lock', type=Path, required=True)
    parser.add_argument('--expected', required=True)
    args = parser.parse_args()
    reuse(args.cache, args.source, json.loads(args.lock.read_text()), args.expected)


if __name__ == '__main__':
    main()
