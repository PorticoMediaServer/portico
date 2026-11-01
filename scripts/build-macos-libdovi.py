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


# The exact same tool implementation serves native Mac and Linux engines.
import importlib.util
shared_file = Path(__file__).with_name('portico-rust-tools.py')
if not shared_file.is_file():
    shared_file = Path(__file__).with_name('ffmpeg-rust-tools.py')
shared_spec = importlib.util.spec_from_file_location('portico_native_rust_tools', shared_file)
shared = importlib.util.module_from_spec(shared_spec)
shared_spec.loader.exec_module(shared)
digest = shared.digest
checked = shared.checked
shared_versions = shared.shared_versions
download = shared.download
extract = shared.extract
write_json = shared.write_json
vendor = shared.vendor
tools = shared.tools


def build(target, recipe, prefix, work):
    machine, triple = TARGETS[target]
    if platform.system() != 'Darwin' or platform.machine() != machine:
        raise ValueError('Matching native macOS runner required')
    source_lock = recipe / 'portico-ffmpeg-sources.lock.json'
    lock = json.loads(source_lock.read_text())
    tools_file = recipe / 'portico-native-rust-tools.lock.json'
    checked(tools_file, lock['nativeRustTools']['sha256'])
    pin = json.loads(tools_file.read_text())
    shared_versions(pin, recipe / 'portico-media-engine-sources.lock.json')
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
    tools_pin = ROOT / lock['nativeRustTools']['path']; checked(tools_pin, lock['nativeRustTools']['sha256'])
    media_lock = ROOT / 'third_party/media-engine/sources.lock.json'
    shared_versions(json.loads(tools_pin.read_text()), media_lock)
    shutil.copyfile(media_lock, recipe / 'portico-media-engine-sources.lock.json')
    shutil.copyfile(tools_pin, recipe / 'portico-native-rust-tools.lock.json')
    shutil.copyfile(ROOT / 'scripts/ffmpeg-rust-tools.py', recipe / 'portico-rust-tools.py')
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
