#!/usr/bin/env python3
"""Private native Rust/cargo-c tools from exact official source/distribution pins."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import urllib.request


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def checked(path, expected):
    if path.is_symlink() or digest(path) != expected:
        raise ValueError('Native libdovi input changed: ' + str(path))


def shared_versions(pin, media_lock):
    versions = json.loads(media_lock.read_text())['tools']
    if (pin['rust']['version'] != versions['rust']['version'] or
            pin['cargoC']['version'].split('+', 1)[0] != versions['cargo-c']['version']):
        raise ValueError('Native Rust/cargo-c pins differ from the repository media-engine tools')


def download(entry, path):
    path.parent.mkdir(parents=True, exist_ok=True)
    if not path.exists():
        temporary = path.with_suffix('.new')
        with urllib.request.urlopen(entry['url'], timeout=60) as source, temporary.open('wb') as out:
            shutil.copyfileobj(source, out)
        checked(temporary, entry['sha256'])
        temporary.replace(path)
    checked(path, entry['sha256'])
    return path


def extract(archive, destination):
    destination.mkdir()
    with tarfile.open(archive) as stream:
        stream.extractall(destination, filter='data')
    roots = list(destination.iterdir())
    if len(roots) != 1 or not roots[0].is_dir() or roots[0].is_symlink():
        raise ValueError('Expected one native tool/source archive root')
    return roots[0]


def write_json(path, data):
    temporary = path.with_suffix('.new')
    temporary.write_text(json.dumps(data, indent=2) + '\n')
    temporary.replace(path)


def vendor(tree, destination, env, sync=None):
    command = ['cargo', 'vendor', '--locked', '--versioned-dirs']
    if sync:
        command += ['--sync', sync]
    command.append(str(destination))
    config = subprocess.check_output(command, cwd=tree, env=env, text=True)
    (tree / '.cargo').mkdir(exist_ok=True)
    (tree / '.cargo/config.toml').write_text(config)


def tools(pin, target, work, stage):
    prefix = work / 'prefix'
    if prefix.exists():
        raise ValueError('Native tools must start in a fresh owned prefix')
    work.mkdir(parents=True, exist_ok=True)
    # Every compiler component is selected from one sealed official manifest.
    manifest = download(pin['rust']['distributionManifest'], stage / 'rust-distribution.toml')
    import tomllib
    distribution = tomllib.loads(manifest.read_text())
    if distribution['pkg']['rustc']['git_commit_hash'] != pin['rust']['commit']:
        raise ValueError('Rust compiler/source commit mismatch')
    for name, entry in {**pin['rust']['targets'][target], 'rust-src': pin['rust']['source']}.items():
        upstream = distribution['pkg'][name]['target']['*' if name == 'rust-src' else target]
        if (entry['url'], entry['sha256']) != (upstream['xz_url'], upstream['xz_hash']):
            raise ValueError('Rust component differs from the sealed distribution manifest')
        # Only matching library source ships in corresponding source; compiler
        # binaries and their transient extraction stay outside the recipe tree.
        archive = download(entry, (stage if name == 'rust-src' else work) / (name + '.tar.xz'))
        tree = extract(archive, work / (name + '-unpack'))
        subprocess.run(['bash', tree / 'install.sh', '--prefix=' + str(prefix), '--disable-ldconfig'], check=True)
        shutil.rmtree(tree.parent)
    env = dict(os.environ, PATH=str(prefix / 'bin') + os.pathsep + os.environ['PATH'],
               CARGO_HOME=str(work / 'cargo-home'), CARGO_TARGET_DIR=str(work / 'target'),
               CARGO_BUILD_JOBS='2', CARGO_INCREMENTAL='0', RUSTFLAGS='')
    # cargo-c is a host tool. Never let an engine cross-linker/prefix or the
    # compiler image's nightly Cargo configuration build its host dependencies.
    for name in ('RUSTC', 'RUSTC_WRAPPER', 'RUSTC_WORKSPACE_WRAPPER', 'RUSTUP_TOOLCHAIN', 'CARGO_BUILD_TARGET',
                 'CARGO_ENCODED_RUSTFLAGS', 'CARGO_TARGET_X86_64_UNKNOWN_LINUX_GNU_LINKER',
                 'CPPFLAGS', 'CFLAGS', 'CXXFLAGS', 'LDFLAGS', 'LIBRARY_PATH', 'LD'):
        env.pop(name, None)
    env.update(CC='/usr/bin/cc', CXX='/usr/bin/c++', AR='/usr/bin/ar', RANLIB='/usr/bin/ranlib',
               PKG_CONFIG_PATH='', PKG_CONFIG_LIBDIR='')
    actual = subprocess.check_output(['rustc', '-vV'], env=env, text=True)
    if ('release: ' + pin['rust']['version'] + '\n' not in actual or
            'commit-hash: ' + pin['rust']['commit'] + '\n' not in actual or
            'host: ' + target + '\n' not in actual):
        raise ValueError('Installed native Rust identity differs from pin')
    archive = download(pin['cargoC'], stage / 'cargo-c.crate')
    crate = extract(archive, stage / 'cargo-c-unpack')
    checked(crate / 'Cargo.lock', pin['cargoC']['cargoLockSHA256'])
    vendor(crate, stage / 'cargo-c-vendor', env)
    checked(crate / 'Cargo.lock', pin['cargoC']['cargoLockSHA256'])
    subprocess.run(['cargo', 'install', '--path', str(crate), '--locked', '--offline',
                    '--features=vendored-openssl', '--root', str(prefix), '-j2'], cwd=crate, env=env, check=True)
    version = subprocess.check_output(['cargo-capi', '--version'], env=env, text=True).strip()
    if pin['cargoC']['version'] not in version:
        raise ValueError('Installed cargo-c identity differs from pin')
    write_json(stage / 'native-tools.json', {'rustc': actual, 'cargoC': version,
        'pin': pin, 'target': target, 'release': False,
        'builderSHA256': digest(Path(__file__)),
        'executables': {name: digest(prefix / 'bin' / name) for name in ('rustc', 'cargo', 'cargo-capi')},
        'rustLibrarySource': 'rust-src.tar.xz'})
    return env
