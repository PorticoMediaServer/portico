#!/usr/bin/env python3
"""One hash-pinned Meson input and installed-tool receipt for every target."""
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


def recipe(directory, requirements):
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
    parser.add_argument('mode', choices=('recipe', 'receipt', 'macos-timing'))
    parser.add_argument('--requirements', type=Path, required=True)
    parser.add_argument('--recipe', type=Path)
    parser.add_argument('--python', type=Path)
    parser.add_argument('--output', type=Path)
    args = parser.parse_args()
    if args.mode in ('recipe', 'macos-timing'):
        if args.recipe is None:
            parser.error('recipe directory required')
        if args.mode == 'recipe':
            recipe(args.recipe, args.requirements)
        else:
            macos_timing(args.recipe)
    else:
        if args.python is None or args.output is None:
            parser.error('Python executable and output required')
        receipt(args.python, args.requirements, args.output)
