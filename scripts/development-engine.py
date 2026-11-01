#!/usr/bin/env python3
"""A development FFmpeg engine for internal releases: NOT FOR USE.

Until the qualified FFmpeg toolchain workflow is ported (lane D), internal 0.0.N releases carry
upstream jellyfin-ffmpeg's portable build, pinned by SHA-256 here. It is never a Portico release
engine:
  - its toolchain-manifest.json says "development": true and lacks the qualified provenance, so
    scripts/verify-ffmpeg-manifest.py, the server's own bundle verification and every sealed
    release path refuse it;
  - build-server-release.py accepts it only with --unsealed, and the assets say NOT-FOR-USE-ENGINE.
It has no portico-disc helper, so disc reading is unavailable, and none of the qualified HDR proofs.

  development-engine.py fetch --target linux-x64 --output DIR [--cache DIR]
  development-engine.py verify DIR [--target linux-x64]
  development-engine.py source --output FILE [--cache DIR]   (the upstream source archive, for the GPL)
"""
import argparse
import hashlib
import io
import json
import pathlib
import shutil
import sys
import tarfile
import urllib.request
import zipfile

UPSTREAM = 'jellyfin/jellyfin-ffmpeg'
TAG = 'v8.1.3-1'
BASE = f'https://github.com/{UPSTREAM}/releases/download/{TAG}/'
PINS = {
    'linux-x64': ('jellyfin-ffmpeg_8.1.3-1_portable_linux64-gpl.tar.xz', 'b86dc023c64a5a9a410e6e3a938a970460214fae78f31c1553c9f88f1c289b6a'),
    'linux-arm64': ('jellyfin-ffmpeg_8.1.3-1_portable_linuxarm64-gpl.tar.xz', '7bc8e8d0986f7f4693f63e9728570f671f07b6e21c05d5465dd7ce461d1631dc'),
    'macos-arm64': ('jellyfin-ffmpeg_8.1.3-1_portable_macarm64-gpl.tar.xz', '22445d7299742749ad2eeb9ce87963d50def0357e45b3e6c7b69987c8365dbf6'),
    'windows-x64': ('jellyfin-ffmpeg_8.1.3-1_portable_win64-clang-gpl.zip', '3691f8b2d4c39c511b5faff1c8c4687bd782dac823e63ba69f0755f7ce1af9ce'),
    'windows-arm64': ('jellyfin-ffmpeg_8.1.3-1_portable_winarm64-clang-gpl.zip', '4118c7046bf3410e342b6cf583ccdae01b940df3c2b3e3f7d329d34569da3283'),
}
SOURCE = (f'https://github.com/{UPSTREAM}/archive/refs/tags/{TAG}.tar.gz', 'jellyfin-ffmpeg-8.1.3-1-source.tar.gz',
          '87bedcefc860cd234cdeddbebdcd3ce45aeedc81d0fbb258ed839ff778be3140')
MAX_ARCHIVE = 512 << 20
MAX_TOOL = 512 << 20
LABEL = 'development engine, NOT FOR USE'


def fail(message):
    raise SystemExit('development-engine: ' + message)


def digest(data):
    return hashlib.sha256(data).hexdigest()


def fetch_pinned(url, name, sha256, cache):
    cached = cache / name if cache else None
    if cached and cached.is_file():
        data = cached.read_bytes()
        if digest(data) == sha256:
            return data
    with urllib.request.urlopen(url, timeout=300) as response:
        data = response.read(MAX_ARCHIVE + 1)
    if len(data) > MAX_ARCHIVE:
        fail(f'{name} is larger than {MAX_ARCHIVE} bytes')
    if digest(data) != sha256:
        fail(f'{name} does not match its pinned SHA-256')
    if cached:
        cache.mkdir(parents=True, exist_ok=True)
        cached.write_bytes(data)
    return data


def tools(target):
    suffix = '.exe' if target.startswith('windows-') else ''
    return ['ffmpeg' + suffix, 'ffprobe' + suffix]


def extract(target, archive_name, data):
    """Only the two executables, by exact name at the archive root; anything else is refused."""
    wanted, found = set(tools(target)), {}
    if archive_name.endswith('.zip'):
        with zipfile.ZipFile(io.BytesIO(data)) as z:
            for info in z.infolist():
                if info.is_dir():
                    continue
                if info.filename not in wanted or info.file_size > MAX_TOOL:
                    fail(f'unexpected archive member {info.filename!r}')
                found[info.filename] = z.read(info)
    else:
        with tarfile.open(fileobj=io.BytesIO(data), mode='r:xz') as t:
            for member in t.getmembers():
                if member.isdir():
                    continue
                if not member.isfile() or member.name not in wanted or member.size > MAX_TOOL:
                    fail(f'unexpected archive member {member.name!r}')
                found[member.name] = t.extractfile(member).read()
    if set(found) != wanted:
        fail(f'{archive_name} lacks {sorted(wanted - set(found))}')
    return found


NOTICE = '''# Development FFmpeg engine: NOT FOR USE

This build of Portico Media Server carries upstream jellyfin-ffmpeg {tag} ({asset}), unmodified,
in place of Portico's qualified FFmpeg toolchain. It is for internal testing only.

- Licence: GNU General Public License, version 3 or later (the "-gpl" build); see LICENSES/.
- Corresponding source: {source_name} (SHA-256 {source_sha}), attached to the same release, from
  https://github.com/{upstream}/releases/tag/{tag}.
- Not included: Portico's disc helper (portico-disc) and the qualified HDR and Dolby Vision proofs.
  Disc reading is unavailable, and the server reports this engine as unqualified.
'''


def fetch(target, output, cache):
    if target not in PINS:
        fail(f'no pinned upstream build for {target}')
    asset, sha256 = PINS[target]
    data = fetch_pinned(BASE + asset, asset, sha256, cache)
    found = extract(target, asset, data)
    source = fetch_pinned(SOURCE[0], SOURCE[1], SOURCE[2], cache)
    licence = source_licence(source)
    if output.exists():
        fail(f'{output} already exists')
    (output / 'bin').mkdir(parents=True)
    (output / 'LICENSES').mkdir()
    for name, content in found.items():
        path = output / 'bin' / name
        path.write_bytes(content)
        path.chmod(0o755)
    (output / 'LICENSES' / 'ffmpeg-GPL-3.0.txt').write_bytes(licence)
    (output / 'NOTICE.md').write_text(NOTICE.format(tag=TAG, asset=asset, source_name=SOURCE[1], source_sha=SOURCE[2], upstream=UPSTREAM))
    files = {p.relative_to(output).as_posix(): digest(p.read_bytes()) for p in sorted(output.rglob('*')) if p.is_file()}
    manifest = {
        'schemaVersion': 1, 'licenseMode': 'gpl3', 'target': target,
        'development': True, 'qualification': LABEL,
        'upstream': {'repository': UPSTREAM, 'tag': TAG, 'asset': asset, 'sha256': sha256,
                     'source': {'name': SOURCE[1], 'sha256': SOURCE[2]}},
        'files': files,
    }
    (output / 'toolchain-manifest.json').write_text(json.dumps(manifest, indent=2, sort_keys=True) + '\n')
    return manifest


def source_licence(source):
    with tarfile.open(fileobj=io.BytesIO(source), mode='r:gz') as t:
        for member in t.getmembers():
            parts = member.name.split('/')
            if member.isfile() and len(parts) == 2 and parts[1] == 'COPYING.GPLv3':
                return t.extractfile(member).read()
    fail('the upstream source archive has no COPYING.GPLv3')


def verify(root, target=None):
    """The development bundle exactly as fetch wrote it: pinned upstream, every file hashed."""
    m = json.loads((root / 'toolchain-manifest.json').read_text())
    if m.get('development') is not True or m.get('qualification') != LABEL:
        fail('not a development engine bundle')
    t = m.get('target')
    if t not in PINS or (target and t != target):
        fail(f'bundle target {t!r} is not {target!r}')
    up = m.get('upstream', {})
    if (up.get('repository'), up.get('tag'), up.get('asset'), up.get('sha256')) != (UPSTREAM, TAG, *PINS[t]):
        fail('bundle does not name the pinned upstream build')
    actual = {p.relative_to(root).as_posix() for p in root.rglob('*') if p.is_file() and p.name != 'toolchain-manifest.json'}
    expected = {'bin/' + n for n in tools(t)} | {'NOTICE.md', 'LICENSES/ffmpeg-GPL-3.0.txt'}
    if actual != expected or set(m['files']) != expected:
        fail('bundle file inventory differs')
    for name, sha in m['files'].items():
        path = root / name
        if path.is_symlink() or digest(path.read_bytes()) != sha:
            fail(f'{name} changed')
    return m


def main(argv=None):
    p = argparse.ArgumentParser()
    sub = p.add_subparsers(dest='command', required=True)
    f = sub.add_parser('fetch'); f.add_argument('--target', required=True, choices=sorted(PINS)); f.add_argument('--output', required=True, type=pathlib.Path); f.add_argument('--cache', type=pathlib.Path)
    v = sub.add_parser('verify'); v.add_argument('bundle', type=pathlib.Path); v.add_argument('--target', choices=sorted(PINS))
    s = sub.add_parser('source'); s.add_argument('--output', required=True, type=pathlib.Path); s.add_argument('--cache', type=pathlib.Path)
    a = p.parse_args(argv)
    if a.command == 'fetch':
        print(json.dumps(fetch(a.target, a.output, a.cache)['upstream']))
    elif a.command == 'verify':
        print(verify(a.bundle.resolve(), a.target)['target'])
    else:
        a.output.write_bytes(fetch_pinned(SOURCE[0], SOURCE[1], SOURCE[2], a.cache))
        print(a.output)


if __name__ == '__main__':
    sys.exit(main())
