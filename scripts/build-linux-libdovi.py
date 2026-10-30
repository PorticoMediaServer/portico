#!/usr/bin/env python3
"""Build the qualified libdovi source and a test-only oracle after base libraries.

The compiler image supplies Rust/cargo-c, never a prebuilt media library. Cargo
verifies the locked registry checksums; the complete vendored source and notices
are retained for offline reproduction. Only one bounded compiler runs at a time.
"""
import argparse
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


def atomic_json(path, value):
    temporary = path.with_suffix(path.suffix + '.new')
    temporary.write_text(json.dumps(value, indent=2, sort_keys=True) + '\n')
    temporary.replace(path)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--work', type=Path, required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    scratch = Path(os.environ['PORTICO_LANE_SCRATCH']).resolve(strict=True)
    work = args.work.resolve(strict=True)
    if not work.is_relative_to(scratch):
        raise ValueError('libdovi must stay in the lane scratch directory')
    lock = json.loads((root / 'third_party/ffmpeg/sources.lock.json').read_text())
    source = lock['libdovi']
    source_network = os.environ.get('PORTICO_BUILD_SOURCE_NETWORK', 'bridge')
    if source_network not in ('bridge', 'host'):
        raise ValueError('Unsupported public source-intake container network')
    supplement_file = root / source['licenseSupplements']['path']
    if digest(supplement_file) != source['licenseSupplements']['sha256']:
        raise ValueError('Cargo license supplement manifest changed')
    supplements = json.loads(supplement_file.read_text())
    for module in supplements['modules'].values():
        for notice in module['files']:
            path = (supplement_file.parent / notice['path']).resolve(strict=True)
            if not path.is_relative_to(supplement_file.parent) or digest(path) != notice['sha256']:
                raise ValueError('Cargo upstream license supplement changed: ' + notice['path'])
    contract_file = root / source['contract']
    if digest(contract_file) != source['contractSHA256']:
        raise ValueError('libdovi qualified source contract changed')
    contract = json.loads(contract_file.read_text())
    for key, field in [('source', 'archiveUrl'), ('sha256', 'sha256'), ('commit', 'gitCommit')]:
        if source[key] != contract['source'][field]:
            raise ValueError('libdovi lock/qualified source mismatch: ' + key)
    deps = work / 'linux-deps'
    prefix = deps / 'prefix'
    stage = work / 'libdovi'
    stage.mkdir(exist_ok=True)
    expected = {'sourceSHA256': source['sha256'], 'sourceCommit': source['commit'],
                'contractSHA256': source['contractSHA256'], 'cargoLocks': source['cargoLocks'],
                'licenseSupplementsSHA256': source['licenseSupplements']['sha256'],
                'compilerImage': lock['linuxSourceBuild']['compilerImage'],
                'sourceFetchNetwork': source_network,
                'builderSHA256': digest(Path(__file__))}
    record_file = prefix / 'libdovi-build.json'
    if record_file.exists():
        record = json.loads(record_file.read_text())
        if any(record.get(key) != value for key, value in expected.items()):
            raise ValueError('libdovi resume inputs changed; preserve the old prefix')
        for name, sha in record['installedFiles'].items():
            if digest(prefix / name) != sha:
                raise ValueError('libdovi installed input changed: ' + name)
        if digest(stage / 'oracle/dovi_tool') != record['oracleSHA256']:
            raise ValueError('Official test oracle bytes changed')
        if digest(stage / 'vendor-source.tar.xz') != record['vendorSourceSHA256']:
            raise ValueError('libdovi vendored source changed')
        print('Reused exact source-built libdovi and test oracle')
        return
    cache = scratch / 'libdovi/sources'
    cache.mkdir(parents=True, exist_ok=True)
    archive = cache / ('dovi_tool-' + source['commit'][:8] + '.tar.gz')
    if not archive.exists():
        temporary = archive.with_suffix(archive.suffix + '.new')
        urllib.request.urlretrieve(source['source'], temporary)
        if digest(temporary) != source['sha256']:
            raise ValueError('libdovi archive checksum mismatch')
        temporary.replace(archive)
    if digest(archive) != source['sha256']:
        raise ValueError('libdovi source archive changed')
    tree = stage / 'source'
    if not tree.exists():
        unpack = stage / 'unpack'
        unpack.mkdir(exist_ok=True)
        with tarfile.open(archive) as tar:
            tar.extractall(unpack, filter='data')
        roots = list(unpack.iterdir())
        if len(roots) != 1 or not roots[0].is_dir():
            raise ValueError('expected one libdovi archive root')
        roots[0].rename(tree)
        unpack.rmdir()
    for name, sha in source['cargoLocks'].items():
        if digest(tree / name) != sha:
            raise ValueError('libdovi Cargo lock mismatch: ' + name)
    if digest(tree / contract['source']['licensePath']) != contract['source']['licenseSha256']:
        raise ValueError('libdovi MIT license mismatch')
    # A failed attempt's compiler objects are disposable; source/vendor inputs
    # remain intact. Both builds use a single private, bounded target directory.
    if (stage / 'build').exists():
        shutil.rmtree(stage / 'build')
    for name in ('tmp', 'build', 'cargo-home'):
        (stage / name).mkdir(exist_ok=True)
    (stage / 'contract.json').write_text(contract_file.read_text())
    (stage / 'expected.json').write_text(json.dumps(expected))
    shutil.copytree(supplement_file.parent, stage / 'license-supplements', dirs_exist_ok=True)
    frozen_builder = stage / 'builder.py'
    shutil.copyfile(Path(__file__), frozen_builder)
    if digest(frozen_builder) != expected['builderSHA256']:
        raise ValueError('libdovi builder changed during capture')
    script = stage / 'build.sh'
    script.write_text('''#!/bin/bash
set -euo pipefail
portico_output_owner="$(stat -c '%u:%g' /dovi)"
portico_restore_owner() {
    portico_stage_status=$?
    trap - EXIT
    chown -hRP "$portico_output_owner" /dovi /opt/ffbuild
    exit "$portico_stage_status"
}
trap portico_restore_owner EXIT
export LC_ALL=C.UTF-8 TZ=UTC ZERO_AR_DATE=1
export TMPDIR=/dovi/tmp CARGO_HOME=/dovi/cargo-home CARGO_TARGET_DIR=/dovi/build
export CARGO_BUILD_JOBS=2 CARGO_TARGET_X86_64_UNKNOWN_LINUX_GNU_LINKER="$CC"
export RUSTFLAGS='--remap-path-prefix=/dovi=/usr/src/libdovi -Crelocation-model=pic -Ctarget-cpu=x86-64'
export PKG_CONFIG_PATH= PKG_CONFIG_LIBDIR=/opt/ffbuild/lib/pkgconfig:/opt/ffbuild/share/pkgconfig
cd /dovi/source
mkdir -p .cargo
cargo cinstall --locked --offline --manifest-path dolby_vision/Cargo.toml --features capi \
 --release --library-type staticlib --target x86_64-unknown-linux-gnu --prefix /opt/ffbuild --libdir lib -j2
# This official source-built CLI is a fixture oracle only, not a server runtime
# dependency or distributed bundle executable. Internal fonts avoid host fonts.
cargo build --locked --offline --release --target x86_64-unknown-linux-gnu \
 --no-default-features --features internal-font --bin dovi_tool -j2
mkdir -p /dovi/oracle
cp /dovi/build/x86_64-unknown-linux-gnu/release/dovi_tool /dovi/oracle/dovi_tool
python3 /builder.py --inside
tar --sort=name --mtime="@$SOURCE_DATE_EPOCH" --owner=0 --group=0 --numeric-owner \
 -C /dovi -I 'xz -T1' -cf /dovi/vendor-source.tar.xz vendor
''')
    fetch = stage / 'fetch.sh'
    fetch.write_text('''#!/bin/bash
set -euo pipefail
portico_output_owner="$(stat -c '%u:%g' /dovi)"
portico_restore_owner() {
    portico_stage_status=$?
    trap - EXIT
    chown -hRP "$portico_output_owner" /dovi
    exit "$portico_stage_status"
}
trap portico_restore_owner EXIT
export TMPDIR=/dovi/tmp CARGO_HOME=/dovi/cargo-home LC_ALL=C.UTF-8 TZ=UTC
cd /dovi/source
mkdir -p .cargo
cargo vendor --locked --sync dolby_vision/Cargo.toml --versioned-dirs /dovi/vendor > .cargo/config.toml
''')
    vendor_lock = stage / 'vendor-lock.json'
    if vendor_lock.exists():
        vendor = json.loads(vendor_lock.read_text())
        if (vendor.get('sourceSHA256') != source['sha256'] or vendor.get('cargoLocks') != source['cargoLocks'] or
                digest(stage / 'vendor-source.tar.xz') != vendor.get('vendorSourceSHA256')):
            raise ValueError('Offline vendor archive differs from its qualified source lock')
        with tarfile.open(stage / 'vendor-source.tar.xz') as tar:
            if any(not m.name.startswith('vendor/') and m.name != 'vendor' for m in tar.getmembers()):
                raise ValueError('Vendor source escaped its archive root')
            tar.extractall(stage, filter='data')
        (tree / '.cargo').mkdir(exist_ok=True)
        (tree / '.cargo/config.toml').write_text('[source.crates-io]\nreplace-with = "vendored-sources"\n'
            '[source.vendored-sources]\ndirectory = "/dovi/vendor"\n')
    else:
        subprocess.run(['docker', 'run', '--rm', '--name', 'portico-hardware-r29-libdovi-fetch',
            '--network', source_network,
            '--label', 'portico.lane=hardware-r29', '--cpus', '1', '--memory', '512m', '--memory-swap', '512m',
            '-v', str(stage) + ':/dovi', lock['linuxSourceBuild']['compilerImage'], 'bash', '/dovi/fetch.sh'], check=True)
    subprocess.run(['docker', 'run', '--rm', '--name', 'portico-hardware-r29-libdovi',
        '--label', 'portico.lane=hardware-r29', '--cpus', '2', '--memory', '4g', '--memory-swap', '4g', '--network', 'none',
        '-v', str(stage) + ':/dovi', '-v', str(prefix) + ':/opt/ffbuild',
        '-v', str(frozen_builder) + ':/builder.py:ro',
        '-e', 'SOURCE_DATE_EPOCH=' + str(lock['sourceDateEpoch']),
        lock['linuxSourceBuild']['compilerImage'], 'bash', '/dovi/build.sh'], check=True)
    installed = ['lib/libdovi.a', 'include/libdovi/rpu_parser.h', 'lib/pkgconfig/dovi.pc']
    if digest(prefix / installed[1]) != contract['cAbi']['headerSha256']:
        raise ValueError('libdovi generated C ABI header differs from qualified source')
    record = {**expected, **json.loads((stage / 'compiler-and-licenses.json').read_text()),
              'installedFiles': {name: digest(prefix / name) for name in installed},
              'vendorSourceSHA256': digest(stage / 'vendor-source.tar.xz'),
              'oracleSHA256': digest(stage / 'oracle/dovi_tool'),
              'sourceArchive': archive.name,
              'scope': 'static libdovi plus test-only official CLI; packet/video gates still required'}
    atomic_json(vendor_lock, {'sourceSHA256': source['sha256'], 'cargoLocks': source['cargoLocks'],
                             'vendorSourceSHA256': record['vendorSourceSHA256']})
    atomic_json(record_file, record)
    # Refresh the canonical seal only after the new installed inputs qualified.
    atomic_json(deps / 'prefix-files.json', {str(p.relative_to(prefix)): digest(p)
                for p in sorted(prefix.rglob('*')) if p.is_file()})
    shutil.rmtree(stage / 'build')
    shutil.rmtree(stage / 'cargo-home')
    print('Built locked libdovi 3.4.0 C ABI and official test oracle from source')


def inside():
    import tomllib
    stage, prefix = Path('/dovi'), Path('/opt/ffbuild')
    contract = json.loads((stage / 'contract.json').read_text())
    expected = json.loads((stage / 'expected.json').read_text())
    supplement_file = stage / 'license-supplements/manifest.json'
    if digest(supplement_file) != expected['licenseSupplementsSHA256']:
        raise ValueError('Frozen Cargo license supplements changed')
    supplements = json.loads(supplement_file.read_text())['modules']
    version = subprocess.check_output(['pkg-config', '--modversion', 'dovi'], text=True).strip()
    if version != contract['source']['crateVersion']:
        raise ValueError('libdovi pkg-config version mismatch: ' + version)
    notices = prefix / 'LICENSES/libdovi'
    notices.mkdir(parents=True, exist_ok=True)
    modules = []
    for directory in [stage / 'source/dolby_vision', *sorted((stage / 'vendor').iterdir())]:
        metadata = tomllib.loads((directory / 'Cargo.toml').read_text())['package']
        paths = []
        for path in sorted(directory.rglob('*')):
            if not path.is_file() or path.is_symlink():
                continue
            relative = path.relative_to(directory)
            if (path.name.lower().startswith(('license', 'licence', 'copying', 'copyright', 'notice', 'patent', 'unlicense')) or
                    any(p.lower() in ('license', 'licenses', 'licences') for p in relative.parts[:-1])):
                target = notices / (metadata['name'] + '-' + metadata['version']) / relative
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(path, target)
                paths.append({'path': str(target.relative_to(prefix)), 'sha256': digest(path)})
        declared_file = metadata.get('license-file')
        if declared_file:
            source_notice = (directory / declared_file).resolve(strict=True)
            if not source_notice.is_relative_to(directory):
                raise ValueError('Crate license-file escaped its verified source')
            relative = source_notice.relative_to(directory)
            target = notices / (metadata['name'] + '-' + metadata['version']) / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(source_notice, target)
            if not any(item['path'] == str(target.relative_to(prefix)) for item in paths):
                paths.append({'path': str(target.relative_to(prefix)), 'sha256': digest(source_notice)})
        supplement = None
        if not paths:
            supplement = supplements.get(metadata['name'] + '-' + metadata['version'])
            if (not supplement or supplement['name'] != metadata['name'] or
                    supplement['version'] != metadata['version'] or supplement['license'] != metadata.get('license') or
                    supplement['crateCargoSHA256'] != digest(directory / 'Cargo.toml')):
                raise ValueError('Actual crate license text/declaration missing: ' + metadata['name'])
            for notice in supplement['files']:
                source_notice = (supplement_file.parent / notice['path']).resolve(strict=True)
                if (not source_notice.is_relative_to(supplement_file.parent) or
                        digest(source_notice) != notice['sha256']):
                    raise ValueError('Frozen upstream license text changed')
                target = notices / directory.name / notice['path']
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(source_notice, target)
                paths.append({'path': str(target.relative_to(prefix)), 'sha256': digest(source_notice),
                              'upstreamURL': notice['url'], 'upstreamCommit': notice['upstreamCommit']})
            # Preserve the exact authors/declaration and all actual source
            # copyright notices without rewriting the checksum-locked vendor.
            target = notices / directory.name / 'Cargo.toml'
            shutil.copyfile(directory / 'Cargo.toml', target)
            paths.append({'path': str(target.relative_to(prefix)), 'sha256': digest(target)})
            witness = supplement.get('sourceNotice')
            if witness:
                source_notice = directory / witness['path']
                if digest(source_notice) != witness['sha256']:
                    raise ValueError('Original crate source notice changed')
                target = notices / directory.name / 'SOURCE-NOTICE.txt'
                target.write_text('\n'.join(source_notice.read_text().splitlines()[:witness['firstLines']]) + '\n')
                paths.append({'path': str(target.relative_to(prefix)), 'sha256': digest(target),
                              'originalSourceSHA256': witness['sha256'], 'sourcePath': witness['path']})
        if not paths or not (metadata.get('license') or declared_file):
            raise ValueError('Actual crate license text/declaration missing: ' + metadata['name'])
        modules.append({'name': metadata['name'], 'version': metadata['version'],
                        'license': metadata.get('license'), 'licenseFile': metadata.get('license-file'),
                        'upstreamLicenseSupplement': supplement,
                        'notices': paths})
    sysroot = Path(subprocess.check_output(['rustc', '--print', 'sysroot'], text=True).strip())
    rust_licenses = sysroot / 'share/doc/rust'
    rust_notice_files = [rust_licenses / 'COPYRIGHT-library.html', rust_licenses / 'COPYRIGHT.html']
    rust_notice_files += [p for p in (rust_licenses / 'licenses').rglob('*') if p.is_file()]
    if not all(p.is_file() for p in rust_notice_files[:2]) or len(rust_notice_files) < 3:
        raise ValueError('Rust linked-library license inventory missing')
    rust_notices = {}
    for path in rust_notice_files:
        target = notices / 'rust-runtime' / path.relative_to(rust_licenses)
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(path, target)
        rust_notices[str(target.relative_to(prefix))] = digest(path)
    runtime = sysroot / 'lib/rustlib/x86_64-unknown-linux-gnu/lib'
    record = {'versions': {tool: subprocess.check_output(command, text=True).strip()
                          for tool, command in [('rustc', ['rustc', '-vV']), ('cargo', ['cargo', '--version']),
                                                ('cargo-c', ['cargo-capi', '--version'])]},
              'vendoredModules': modules, 'rustRuntimeNotices': rust_notices,
              'rustStandardLibraryArchiveInventory': {p.name: digest(p) for p in sorted(runtime.glob('*.rlib'))},
              'runtimeInventoryBoundary': 'Compiler standard-library superset, not a claim every archive is linked',
              'oracleFeatures': ['--no-default-features', '--features=internal-font']}
    atomic_json(stage / 'compiler-and-licenses.json', record)


if __name__ == '__main__':
    if os.sys.argv[1:] == ['--inside']:
        inside()
    else:
        main()
