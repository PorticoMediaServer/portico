#!/usr/bin/env python3
"""Retain Windows compiler runtime sources and notices; never qualify an engine."""
import hashlib
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import re


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def verify(root):
    record = json.loads((root / 'compiler-inputs.json').read_text())
    if record.get('schemaVersion') != 1 or record.get('release') is not False:
        raise ValueError('invalid compiler custody record')
    if any(p.is_symlink() for p in root.rglob('*')):
        raise ValueError('compiler source inventory contains a link')
    files = {p.relative_to(root).as_posix(): digest(p) for p in sorted(root.rglob('*'))
             if p.is_file() and p.relative_to(root).as_posix() != 'compiler-inputs.json'}
    if files != record['files']:
        raise ValueError('compiler source/notices inventory changed')
    required = {'crosstool-ng.commit', 'crosstool-ng.config', 'crosstool-ng.tar.xz',
                'rust-library.tar.xz', 'LICENSES/rust/COPYRIGHT-library.html', 'capture.py'}
    if not required <= files.keys() or not any(p.startswith('tarballs/gcc-') for p in files) or not any(
            p.startswith('tarballs/mingw-w64-') for p in files):
        raise ValueError('incomplete compiler corresponding source')
    if not re.fullmatch('[a-f0-9]{40}', (root / 'crosstool-ng.commit').read_text().strip()) or any(
            (root / name).stat().st_size == 0 for name in required):
        raise ValueError('empty/unidentified compiler source or notices')
    if not record.get('gccRuntimeArchives') or not record.get('rustRuntimeArchives'):
        raise ValueError('empty linked-runtime inventory')
    return record


def inside(root=Path('/opt/portico-compiler-source')):
    # This runs in the completed compiler image, after rustup added the matching
    # source component to the already installed toolchain. It never updates it.
    rust_sysroot = Path(subprocess.check_output(['rustc', '--print', 'sysroot'], text=True).strip())
    source = rust_sysroot / 'lib/rustlib/src/rust'
    if not (source / 'library/Cargo.toml').is_file():
        raise ValueError('matching Rust standard-library source is missing')
    subprocess.run(['tar', '-C', source.parent, '--sort=name', '--mtime=@0', '--owner=0', '--group=0',
                    '--numeric-owner', '-I', 'xz -T1 -0', '-cf', root / 'rust-library.tar.xz', 'rust'], check=True)
    docs = rust_sysroot / 'share/doc/rust'
    notices = root / 'LICENSES/rust'
    notices.mkdir(parents=True, exist_ok=True)
    for name in ('COPYRIGHT-library.html', 'COPYRIGHT.html'):
        shutil.copyfile(docs / name, notices / name)
    shutil.copytree(docs / 'licenses', notices / 'licenses')
    target = os.environ['FFBUILD_RUST_TARGET']
    runtime = rust_sysroot / 'lib/rustlib' / target / 'lib'
    rust_archives = {p.name: digest(p) for p in sorted(runtime.glob('*.rlib'))}
    gcc_archives = {}
    for tool, name in [('CC', 'libgcc.a'), ('CC', 'libgcc_eh.a'), ('CXX', 'libstdc++.a')]:
        path = Path(subprocess.check_output(shlex.split(os.environ[tool]) + ['-print-file-name=' + name], text=True).strip())
        if not path.is_absolute() or not path.resolve().is_relative_to('/opt/ct-ng'):
            raise ValueError('compiler runtime escaped its source-built prefix: ' + name)
        gcc_archives[name] = digest(path)
    versions = {name: subprocess.check_output(command, text=True).strip() for name, command in [
        ('rustc', ['rustc', '-vV']), ('cargo', ['cargo', '--version']), ('cargo-c', ['cargo-capi', '--version']),
        ('meson', ['meson', '--version']), ('CC', shlex.split(os.environ['CC']) + ['--version']),
        ('CXX', shlex.split(os.environ['CXX']) + ['--version'])]}
    files = {p.relative_to(root).as_posix(): digest(p) for p in sorted(root.rglob('*'))
             if p.is_file() and p.relative_to(root).as_posix() != 'compiler-inputs.json'}
    record = {'schemaVersion': 1, 'target': target, 'release': False, 'files': files,
        'versions': versions, 'gccRuntimeArchives': gcc_archives, 'rustRuntimeArchives': rust_archives,
        'boundary': 'Compiler runtime source/notices custody; no claim every archive is linked, no tool pin or engine qualification.'}
    temporary = root / 'compiler-inputs.new'
    temporary.write_text(json.dumps(record, indent=2) + '\n')
    temporary.replace(root / 'compiler-inputs.json')
    verify(root)


if __name__ == '__main__':
    inside()
