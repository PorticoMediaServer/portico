#!/usr/bin/env python3
"""Freeze Windows rav1e crate source custody; never resolve during compilation."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import tomllib


def configure(recipe, root=None):
    root = root or Path(__file__).resolve().parents[1]
    entry = json.loads((root / 'third_party/ffmpeg/sources.lock.json').read_text())['btbn']['rav1eCargo']
    relative = Path(entry['resolvedCargoLock']['path'])
    path = root / relative
    if (relative.is_absolute() or '..' in relative.parts or path.is_symlink() or
            not path.resolve().is_relative_to(root.resolve())):
        raise ValueError('rav1e Cargo lock must remain inside the source contract')
    data = path.read_bytes()
    sha = hashlib.sha256(data).hexdigest()
    original_sha = entry['originalCargoLockSHA256']
    if sha != entry['resolvedCargoLock']['sha256'] or not re.fullmatch('[a-f0-9]{64}', original_sha):
        raise ValueError('rav1e Cargo lock hash differs from the reviewed pin')
    packages = tomllib.loads(data.decode())['package']
    versions = {(p['name'], p['version']) for p in packages}
    if not set(entry['resolvedPackages'].items()) <= versions:
        raise ValueError('rav1e Cargo package versions differ from the reviewed pin')
    if any(p.get('source', '').startswith('git+') for p in packages):
        raise ValueError('rav1e requires separately reviewed Git source custody')
    script = recipe / 'scripts.d/50-rav1e.sh'
    text = script.read_text()
    for name, expected in [('SCRIPT_REPO', entry['repository']), ('SCRIPT_COMMIT', entry['commit'])]:
        if text.count(name + '="' + expected + '"') != 1:
            raise ValueError('pinned rav1e source identity changed')
    if (text.count('ffbuild_dockerbuild() {\n') != 1 or text.count('    cargo update cc\n') != 1 or
            text.count('    cargo cinstall -v "${myconf[@]}"\n') != 1 or 'ffbuild_dockerdl()' in text):
        raise ValueError('pinned rav1e Cargo source/build boundary changed')
    inputs = recipe / 'portico-rav1e-inputs'
    inputs.mkdir(exist_ok=True)
    (inputs / 'Cargo.lock').write_bytes(data)
    (inputs / 'contract.json').write_text(json.dumps(entry, indent=2) + '\n')
    source = '''ffbuild_dockerdl() {
    default_dl .
    cat <<'PORTICO_RAV1E_SOURCE'
echo 'ORIGINAL_SHA  Cargo.lock' | sha256sum --check
cp Cargo.lock portico-original-Cargo.lock
cp /portico-rav1e/Cargo.lock Cargo.lock
echo 'RESOLVED_SHA  Cargo.lock' | sha256sum --check
cp /portico-rav1e/contract.json portico-rav1e-source.json
test ! -e .cargo/config && test ! -e .cargo/config.toml
mkdir -p .cargo
export CARGO_HOME="$PWD/.portico-cargo-home"
cargo vendor --locked --versioned-dirs vendor > .cargo/config.toml
python3 -c 'import shutil; shutil.rmtree(".portico-cargo-home")'
PORTICO_RAV1E_SOURCE
}

'''.replace('ORIGINAL_SHA', original_sha).replace('RESOLVED_SHA', sha)
    text = text.replace('ffbuild_dockerbuild() {\n', source + 'ffbuild_dockerbuild() {\n')
    text = text.replace('    # The pinned version is broken, and upstream does not react\n    cargo update cc\n',
                        "    # Source preparation supplies the reviewed resolution and complete vendor tree.\n"
                        "    echo '" + original_sha + "  portico-original-Cargo.lock' | sha256sum --check\n"
                        "    echo '" + sha + "  Cargo.lock' | sha256sum --check\n")
    if 'cargo update cc' in text:
        raise ValueError('pinned rav1e update boundary changed')
    text = text.replace('    cargo cinstall -v "${myconf[@]}"\n',
                        '    cargo cinstall --locked --offline -v "${myconf[@]}"\n')
    script.write_text(text)
    download = recipe / 'download.sh'
    text = download.read_text()
    marker = '-v "$PWD/portico-dovi-inputs":/portico-libdovi:ro'
    if text.count(marker) != 1:
        raise ValueError('pinned source-input mount boundary changed')
    download.write_text(text.replace(marker, marker + ' -v "$PWD/portico-rav1e-inputs":/portico-rav1e:ro'))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('recipe', type=Path)
    args = parser.parse_args()
    configure(args.recipe)
