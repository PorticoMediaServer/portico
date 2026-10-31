#!/usr/bin/env python3
"""Add the already locked libdovi dependency to the Windows source recipe."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil

ROOT = Path(__file__).resolve().parents[1]


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def configure(recipe):
    lock = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())['libdovi']
    contract = ROOT / lock['contract']
    supplements = ROOT / lock['licenseSupplements']['path']
    if digest(contract) != lock['contractSHA256'] or digest(supplements) != lock['licenseSupplements']['sha256']:
        raise ValueError('checked libdovi contract/licence supplements changed')
    for module in json.loads(supplements.read_text())['modules'].values():
        for notice in module['files']:
            path = supplements.parent / notice['path']
            if not path.resolve().is_relative_to(supplements.parent) or digest(path) != notice['sha256']:
                raise ValueError('checked Cargo licence text changed')
    # A root directory named like the dependency would make BtbN's resolver
    # mistake this single-script stage for a script group.
    stage = recipe / 'portico-dovi-inputs'
    stage.mkdir(exist_ok=True)
    shutil.copytree(supplements.parent, stage / 'license-supplements', dirs_exist_ok=True)
    shutil.copyfile(contract, stage / 'contract.json')
    (stage / 'expected.json').write_text(json.dumps({'licenseSupplementsSHA256': lock['licenseSupplements']['sha256']}))
    shutil.copyfile(ROOT / 'scripts/build-linux-libdovi.py', stage / 'collect.py')
    # The checked helper's collector is shared; source/config/compiler proofs
    # stay separate, and its existing Linux call retains identical defaults.
    script = r'''#!/bin/bash
SCRIPT_REPO="https://github.com/quietvoid/dovi_tool.git"
SCRIPT_COMMIT="@COMMIT@"

ffbuild_enabled() { [[ $TARGET == win* ]]; }
ffbuild_depends() { echo base; }

ffbuild_dockerdl() {
    cat <<'PORTICO_DOVI_SOURCE'
mkdir portico-libdovi
cd portico-libdovi
curl --fail --location --retry 3 '@URL@' -o source.tar.gz
echo '@SOURCE_SHA@  source.tar.gz' | sha256sum --check
mkdir source
tar -xzf source.tar.gz --strip-components=1 -C source
python3 -c 'from pathlib import Path; Path("source.tar.gz").unlink()'
cd source
echo '@ROOT_LOCK@  Cargo.lock' | sha256sum --check
echo '@CRATE_LOCK@  dolby_vision/Cargo.lock' | sha256sum --check
mkdir -p .cargo
CARGO_HOME="$PWD/../cargo-home" cargo vendor --locked --sync dolby_vision/Cargo.toml --versioned-dirs ../vendor
cd ..
cp -R /portico-libdovi/license-supplements .
cp /portico-libdovi/contract.json /portico-libdovi/expected.json /portico-libdovi/collect.py .
echo '@COLLECTOR_SHA@  collect.py' | sha256sum --check
echo '@CONTRACT_SHA@  contract.json' | sha256sum --check
echo '@SUPPLEMENTS_SHA@  license-supplements/manifest.json' | sha256sum --check
python3 -c 'import shutil; shutil.rmtree("cargo-home")'
PORTICO_DOVI_SOURCE
}

ffbuild_dockerbuild() {
    local source_root="$PWD"
    mkdir -p tmp cargo-home build source/.cargo
    export TMPDIR="$source_root/tmp" CARGO_HOME="$source_root/cargo-home" CARGO_TARGET_DIR="$source_root/build"
    export CARGO_BUILD_JOBS=2
    local cargo_target="${FFBUILD_RUST_TARGET//-/_}"
    export "CARGO_TARGET_${cargo_target^^}_LINKER=$CC" "CC_${cargo_target}=$CC" "AR_${cargo_target}=$AR"
    printf '[source.crates-io]\nreplace-with = "vendored-sources"\n[source.vendored-sources]\ndirectory = "%s/vendor"\n' "$source_root" > source/.cargo/config.toml
    cd source
    cargo cinstall --locked --offline --manifest-path dolby_vision/Cargo.toml --features capi \
        --release --library-type staticlib --target "$FFBUILD_RUST_TARGET" --prefix "$FFBUILD_PREFIX" \
        --destdir "$FFBUILD_DESTDIR" --libdir lib -j2
    cd "$source_root"
    export PKG_CONFIG_LIBDIR="$FFBUILD_DESTPREFIX/lib/pkgconfig"
    python3 - "$source_root" "$FFBUILD_DESTPREFIX" "$FFBUILD_RUST_TARGET" <<'PORTICO_DOVI_NOTICES'
import hashlib, json, os, pathlib, runpy, sys
stage, prefix, target = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2]), sys.argv[3]
contract = json.loads((stage / 'contract.json').read_text())
header = prefix / 'include/libdovi/rpu_parser.h'
if hashlib.sha256(header.read_bytes()).hexdigest() != contract['cAbi']['headerSha256']:
    raise ValueError('Windows libdovi C ABI header differs from the checked contract')
runpy.run_path(str(stage / 'collect.py'))['inside'](stage, prefix, target)
record = json.loads((stage / 'compiler-and-licenses.json').read_text())
record.pop('oracleFeatures', None)
record.update(target=target, sourceCommit='@COMMIT@', sourceArchiveSHA256='@SOURCE_SHA@',
              testOracleBuilt=False,
              sourceLocks={'Cargo.lock':'@ROOT_LOCK@', 'dolby_vision/Cargo.lock':'@CRATE_LOCK@'})
destination = prefix / 'share/doc/ffmpeg/libdovi-build.json'
destination.parent.mkdir(parents=True, exist_ok=True)
destination.write_text(json.dumps(record, indent=2) + '\n')
PORTICO_DOVI_NOTICES
}

ffbuild_configure() { echo --enable-libdovi; }
ffbuild_unconfigure() { echo --disable-libdovi; }
'''
    values = {'COMMIT': lock['commit'], 'URL': lock['source'], 'SOURCE_SHA': lock['sha256'],
              'ROOT_LOCK': lock['cargoLocks']['Cargo.lock'], 'CRATE_LOCK': lock['cargoLocks']['dolby_vision/Cargo.lock'],
              'COLLECTOR_SHA': digest(stage / 'collect.py'), 'CONTRACT_SHA': lock['contractSHA256'],
              'SUPPLEMENTS_SHA': lock['licenseSupplements']['sha256']}
    for name, value in values.items():
        script = script.replace('@' + name + '@', value)
    (recipe / 'scripts.d/50-portico-libdovi.sh').write_text(script)
    final = recipe / 'scripts.d/zz-final.sh'
    text = final.read_text()
    if text.count('    echo libbluray\n') != 1:
        raise ValueError('pinned final dependency hook changed')
    final.write_text(text.replace('    echo libbluray\n', '    echo libbluray\n    echo portico-libdovi\n'))
    download = recipe / 'download.sh'
    text = download.read_text()
    if text.count('docker run -i ') != 1:
        raise ValueError('pinned dependency download hook changed')
    download.write_text(text.replace('docker run -i ', 'docker run -v "$PWD/portico-dovi-inputs":/portico-libdovi:ro -i ', 1))
    build = recipe / 'build.sh'
    text = build.read_text()
    marker = '    make install install-doc\n'
    if text.count(marker) != 1:
        raise ValueError('pinned final notice copy hook changed')
    build.write_text(text.replace(marker, marker + '    cp -R /opt/ffbuild/LICENSES /ffbuild/prefix/\n'
        '    cp /opt/ffbuild/share/doc/ffmpeg/libdovi-build.json /ffbuild/prefix/share/doc/ffmpeg/\n', 1))
    variant = recipe / 'variants/windows-install-static.sh'
    text = variant.read_text()
    marker = '    cp "$IN"/bin/* "$OUT"/bin\n'
    if text.count(marker) != 1:
        raise ValueError('pinned static Windows notice hook changed')
    variant.write_text(text.replace(marker, marker + '    cp -R "$IN"/LICENSES "$OUT"/\n', 1))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('recipe', type=Path)
    configure(parser.parse_args().recipe.resolve())
