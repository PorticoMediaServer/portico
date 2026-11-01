#!/usr/bin/env python3
"""Shared build-tool pins and actual installed-tool receipts."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess


def pin(path):
    raw = path.read_bytes()
    rows = [line for line in raw.decode().splitlines() if line and not line.startswith('#')]
    if len(rows) != 1:
        raise ValueError('one Meson wheel pin required')
    match = re.fullmatch(r'meson==([0-9]+\.[0-9]+\.[0-9]+) --hash=sha256:([a-f0-9]{64})', rows[0])
    if not match:
        raise ValueError('invalid exact Meson wheel pin')
    return {'version': match[1], 'wheelSHA256': match[2], 'requirementsSHA256': hashlib.sha256(raw).hexdigest()}


def btbn_rust(directory, media_lock=None):
    media_lock = media_lock or Path(__file__).resolve().parents[1] / 'third_party/media-engine/sources.lock.json'
    tools = json.loads(media_lock.read_text())['tools']
    versions = {name: tools[name]['version'] for name in ('rust', 'cargo-c')}
    if any(not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+', value) for value in versions.values()):
        raise ValueError('exact shared Rust/cargo-c versions required')
    base = directory / 'images/base'; dockerfile = base / 'Dockerfile'; text = dockerfile.read_text()
    rust = '-- -y --no-modify-path && '
    cargo = 'cargo install cargo-c && '
    if text.count(rust) != 1 or text.count(cargo) != 1:
        raise ValueError('pinned BtbN Rust/cargo-c installation boundary changed')
    text = text.replace(rust, '-- -y --no-modify-path --default-toolchain ' + versions['rust'] + ' && ')
    text = text.replace(cargo, 'cargo install --locked --version ' + versions['cargo-c'] + ' cargo-c && ')
    text += ('\nCOPY portico-rust-versions.json /opt/portico-rust-versions.json\n'
             'RUN python3 /opt/portico-meson-proof.py rust-receipt '
             '--versions /opt/portico-rust-versions.json --output /opt/portico-rust-tool.json\n')
    (base / 'portico-rust-versions.json').write_text(json.dumps({'versions': versions,
        'authority': 'third_party/media-engine/sources.lock.json'}, indent=2) + '\n')
    dockerfile.write_text(text)


def rust_receipt(versions_file, output):
    pin = json.loads(versions_file.read_text())
    versions = {name: subprocess.check_output(command, text=True).strip() for name, command in (
        ('rust', ['rustc', '--version']), ('cargo-c', ['cargo-capi', '--version']))}
    for name, expected in pin['versions'].items():
        if not re.search(r'\b' + re.escape(expected) + r'(?:\+[^\s]+)?(?:\s|$)', versions[name]):
            raise ValueError('installed BtbN ' + name + ' differs from the shared repository pin')
    output.write_text(json.dumps({'schemaVersion': 1, **pin, 'installedVersions': versions,
        'versionsFileSHA256': hashlib.sha256(versions_file.read_bytes()).hexdigest(),
        'boundary': 'Exact versions; rustup bootstrap remains an independent acceptance hold.'}, indent=2) + '\n')


def recipe(directory, requirements, target=None):
    expected = pin(requirements)
    base = directory / 'images/base'
    path = base / 'Dockerfile'; text = path.read_text()
    marker = 'RUN python3 -m pip install --break-system-packages meson'
    if text.count(marker) != 1:
        raise ValueError('pinned BtbN Meson installation boundary changed')
    shutil.copyfile(requirements, base / 'portico-meson-requirements.txt')
    shutil.copyfile(Path(__file__), base / 'portico-meson-proof.py')
    text = text.replace(marker, 'COPY portico-meson-requirements.txt /opt/portico-meson-requirements.txt\n'
        'COPY portico-meson-proof.py /opt/portico-meson-proof.py\n'
        'RUN python3 -m pip install --break-system-packages --require-hashes --only-binary=:all: --no-deps '
        '-r /opt/portico-meson-requirements.txt && '
        'python3 /opt/portico-meson-proof.py receipt --python /usr/bin/python3 '
        '--requirements /opt/portico-meson-requirements.txt --output /opt/portico-meson-tool.json')
    path.write_text(text)
    (base / 'portico-meson-pin.json').write_text(json.dumps(expected, indent=2) + '\n')
    if target in ('windows-x64', 'windows-arm64', 'linux-arm64'):
        btbn_rust(directory)


def receipt(python, requirements, output):
    expected = pin(requirements)
    actual = subprocess.check_output([str(python), '-c',
        "from importlib.metadata import version; print(version('meson'))"], text=True).strip()
    if actual != expected['version']:
        raise ValueError('installed Meson differs from the shared wheel pin')
    temporary = output.with_suffix('.new')
    temporary.write_text(json.dumps({'schemaVersion': 1, 'tool': 'meson', **expected,
                                   'installedVersion': actual}, indent=2) + '\n')
    temporary.replace(output)


def macos_timing(directory):
    path = directory / 'build-ffmpeg'; text = path.read_text()
    if text.count('build() {\n') != 1 or text.count('build_done() {\n') != 1 or 'portico_time_start()' in text:
        raise ValueError('pinned Homebridge timing boundaries changed')
    functions = '''portico_time_end() {
  if [[ -n ${PORTICO_ENGINE_TIMINGS:-} && -n ${portico_timed_stage:-} ]]; then
    printf 'end\\t%s\\t%s\\n' "$portico_timed_stage" "$(date +%s)" >> "$PORTICO_ENGINE_TIMINGS"
  fi
  portico_timed_stage=
}
portico_time_start() {
  portico_time_end
  portico_timed_stage=$1
  if [[ -n ${PORTICO_ENGINE_TIMINGS:-} ]]; then
    printf 'start\\t%s\\t%s\\n' "$1" "$(date +%s)" >> "$PORTICO_ENGINE_TIMINGS"
  fi
}
'''
    text = text.replace('build() {\n', functions + 'build() {\n  portico_time_start "$1"\n', 1)
    text = text.replace('build_done() {\n', 'build_done() {\n  portico_time_end\n', 1)
    path.write_text(text)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('mode', choices=('recipe', 'receipt', 'macos-timing', 'rust-receipt'))
    parser.add_argument('--requirements', type=Path)
    parser.add_argument('--versions', type=Path)
    parser.add_argument('--target', choices=('windows-x64', 'windows-arm64', 'linux-x64', 'linux-arm64'))
    parser.add_argument('--recipe', type=Path)
    parser.add_argument('--python', type=Path)
    parser.add_argument('--output', type=Path)
    args = parser.parse_args()
    if args.mode == 'rust-receipt':
        if args.versions is None or args.output is None:
            parser.error('versions file and output required')
        rust_receipt(args.versions, args.output)
        raise SystemExit(0)
    if args.requirements is None:
        parser.error('shared Meson requirements required')
    if args.mode in ('recipe', 'macos-timing'):
        if args.recipe is None:
            parser.error('recipe directory required')
        if args.mode == 'recipe':
            recipe(args.recipe, args.requirements, args.target)
        else:
            macos_timing(args.recipe)
    else:
        if args.python is None or args.output is None:
            parser.error('Python executable and output required')
        receipt(args.python, args.requirements, args.output)
