#!/usr/bin/env python3
"""Build the checked libdovi C ABI with private, hash-pinned native Rust tools."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import tarfile
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
TARGETS = {'macos-arm64': ('arm64', 'aarch64-apple-darwin'),
           'macos-x64': ('x86_64', 'x86_64-apple-darwin')}


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def checked(path, expected):
    if path.is_symlink() or digest(path) != expected:
        raise ValueError('Native libdovi input changed: ' + str(path))


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
    for name in ('RUSTC', 'RUSTC_WRAPPER', 'RUSTC_WORKSPACE_WRAPPER', 'RUSTUP_TOOLCHAIN', 'CARGO_BUILD_TARGET'):
        env.pop(name, None)
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
        'executables': {name: digest(prefix / 'bin' / name) for name in ('rustc', 'cargo', 'cargo-capi')},
        'rustLibrarySource': 'rust-src.tar.xz'})
    return env


def build(target, recipe, prefix, work):
    machine, triple = TARGETS[target]
    if platform.system() != 'Darwin' or platform.machine() != machine:
        raise ValueError('Matching native macOS runner required')
    source_lock = recipe / 'portico-ffmpeg-sources.lock.json'
    lock = json.loads(source_lock.read_text())
    tools_file = recipe / 'portico-macos-rust-tools.lock.json'
    checked(tools_file, lock['macosRustTools']['sha256'])
    pin = json.loads(tools_file.read_text())
    source = lock['libdovi']
    stage = recipe / 'packages/portico-libdovi'
    stage.mkdir(parents=True)
    contract = recipe / 'portico-libdovi-inputs/contract.json'
    checked(contract, source['contractSHA256'])
    expected = json.loads(contract.read_text())
    if any(source[key] != expected['source'][field] for key, field in
           [('source', 'archiveUrl'), ('sha256', 'sha256'), ('commit', 'gitCommit')]):
        raise ValueError('Mac libdovi source differs from checked C ABI contract')
    env = tools(pin, triple, work, stage)
    archive = download({'url': source['source'], 'sha256': source['sha256']}, stage / 'source.tar.gz')
    tree = extract(archive, stage / 'unpack')
    tree.rename(stage / 'source'); (stage / 'unpack').rmdir(); tree = stage / 'source'
    for name, sha in source['cargoLocks'].items():
        checked(tree / name, sha)
    checked(tree / expected['source']['licensePath'], expected['source']['licenseSha256'])
    vendor(tree, stage / 'vendor', env, 'dolby_vision/Cargo.toml')
    # Both Cargo lock files are preserved unchanged; all registry sources and
    # notices remain in packages/portico-libdovi in corresponding source.
    for name, sha in source['cargoLocks'].items():
        checked(tree / name, sha)
    env.update(RUSTFLAGS='--remap-path-prefix=' + str(stage) + '=/usr/src/libdovi -Crelocation-model=pic',
               PKG_CONFIG_PATH=str(prefix / 'lib/pkgconfig'), PKG_CONFIG_LIBDIR=str(prefix / 'lib/pkgconfig'))
    subprocess.run(['cargo', 'cinstall', '--locked', '--offline', '--manifest-path', 'dolby_vision/Cargo.toml',
                    '--features', 'capi', '--release', '--library-type', 'staticlib', '--target', triple,
                    '--prefix', str(prefix), '--libdir', 'lib', '-j2'], cwd=tree, env=env, check=True)
    checked(prefix / 'include/libdovi/rpu_parser.h', expected['cAbi']['headerSha256'])
    inputs = recipe / 'portico-libdovi-inputs'
    shutil.copyfile(contract, stage / 'contract.json')
    shutil.copytree(inputs / 'license-supplements', stage / 'license-supplements')
    write_json(stage / 'expected.json', {'licenseSupplementsSHA256': source['licenseSupplements']['sha256']})
    # Re-exec the unchanged shared collector under the exact native tools.
    subprocess.run(['python3', inputs / 'collect.py', '--native-stage', stage, prefix, triple], env=env, check=True)
    record = json.loads((stage / 'compiler-and-licenses.json').read_text())
    record.pop('oracleFeatures', None)
    record.update(target=target, rustTarget=triple, sourceCommit=source['commit'],
                  sourceArchiveSHA256=source['sha256'], sourceLocks=source['cargoLocks'],
                  nativeTools=json.loads((stage / 'native-tools.json').read_text()), testOracleBuilt=False)
    write_json(prefix / 'libdovi-build.json', record)


def configure(recipe):
    lock = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())
    source = lock['libdovi']
    inputs = recipe / 'portico-libdovi-inputs'
    inputs.mkdir()
    contract = ROOT / source['contract']; checked(contract, source['contractSHA256'])
    shutil.copyfile(contract, inputs / 'contract.json')
    supplement = ROOT / source['licenseSupplements']['path']
    checked(supplement, source['licenseSupplements']['sha256'])
    shutil.copytree(supplement.parent, inputs / 'license-supplements')
    # A tiny entry wrapper calls the existing shared collector without changing
    # its Linux defaults or requiring a platform branch inside its notice loops.
    shutil.copyfile(ROOT / 'scripts/build-linux-libdovi.py', inputs / 'shared-collector.py')
    (inputs / 'collect.py').write_text('import pathlib,runpy,sys\n'
        'assert sys.argv[1] == "--native-stage"\n'
        'runpy.run_path(str(pathlib.Path(__file__).with_name("shared-collector.py")))["inside"]('
        'pathlib.Path(sys.argv[2]),pathlib.Path(sys.argv[3]),sys.argv[4])\n')
    tools_pin = ROOT / lock['macosRustTools']['path']; checked(tools_pin, lock['macosRustTools']['sha256'])
    shutil.copyfile(tools_pin, recipe / 'portico-macos-rust-tools.lock.json')
    shutil.copyfile(Path(__file__), recipe / 'portico-build-macos-libdovi.py')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('mode', choices=['configure', 'build'])
    parser.add_argument('--recipe', type=Path, required=True)
    parser.add_argument('--target', choices=TARGETS)
    parser.add_argument('--prefix', type=Path)
    parser.add_argument('--tools', type=Path)
    args = parser.parse_args()
    if args.mode == 'configure':
        configure(args.recipe.resolve(strict=True))
    else:
        if not all((args.target, args.prefix, args.tools)):
            parser.error('build requires --target, --prefix and --tools')
        build(args.target, args.recipe.resolve(strict=True), args.prefix.resolve(strict=True), args.tools.resolve())
