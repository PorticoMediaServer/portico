#!/usr/bin/env python3
"""Qualify actual executable inventories against the bundled target contract."""
import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import subprocess
import sys

TARGETS = {'linux-x64': 'linux-amd64', 'linux-arm64': 'linux-arm64',
           'windows-x64': 'windows-amd64', 'windows-arm64': 'windows-arm64',
           'macos-x64': 'darwin-amd64', 'macos-arm64': 'darwin-arm64'}
ROOT = Path(__file__).resolve().parents[1]


def verifier(name):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


disc = verifier('verify-disc-bundle')
bluray = verifier('verify-ffmpeg-bluray')


def macos_udfread(tool, target, lock, root=ROOT):
    if target not in ('macos-x64', 'macos-arm64'):
        return None
    entry = lock['discHelper']
    manifest = root / entry['fixtureManifest']
    if (manifest.is_symlink() or not manifest.resolve().is_relative_to(root.resolve()) or
            disc.digest(manifest) != entry['fixtureManifestSHA256']):
        raise ValueError('Mac UDF reader fixture authority changed')
    fixtures = [row for row in json.loads(manifest.read_text())['fixtures'] if row['id'] in bluray.IDS]
    if len(fixtures) != 2 or {row['id'] for row in fixtures} != set(bluray.IDS):
        raise ValueError('Mac feature probe requires both BDMV and UDF ISO fixtures')
    paths = {row['id']: disc.fixture_path(manifest.parent, row) for row in fixtures}
    # A configure flag or pc file cannot prove the final FFmpeg/libbluray link
    # retained udfread. Decode the same checked title through both real readers.
    result = bluray.qualify(tool, paths)
    for row in fixtures:
        disc.fixture_path(manifest.parent, row)
    if disc.digest(manifest) != entry['fixtureManifestSHA256']:
        raise ValueError('Mac UDF reader fixtures changed during the feature probe')
    return result


def entries(raw, kind):
    result = set()
    for line in raw.splitlines():
        words = line.split()
        if kind in ('hwaccel', 'bsf', 'protocol'):
            if len(words) == 1 and re.fullmatch(r'[a-zA-Z0-9_]+', words[0]):
                result.add(words[0])
        elif len(words) >= 2 and words[1] != '=' and re.fullmatch(r'[.A-Z|]{1,8}', words[0]):
            result.update(words[1].split(','))
    return result


def missing_features(contract, platform, inventories):
    missing = []
    for feature in contract['features']:
        if not feature['required'] or platform not in feature.get('platforms', [platform]):
            continue
        available = inventories[feature['kind']]
        absent = set(feature['acceptable']) - available
        if feature.get('match') == 'any':
            absent = set(feature['acceptable']) if len(absent) == len(feature['acceptable']) else set()
        missing.extend(feature['id'] + ':' + name for name in sorted(absent))
    return missing


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('root', type=Path)
    parser.add_argument('--report', type=Path)
    args = parser.parse_args()
    manifest = json.loads((args.root / 'toolchain-manifest.json').read_text())
    target = manifest['target']
    if target not in TARGETS:
        parser.error('unknown target')
    tool = args.root / 'bin' / ('ffmpeg.exe' if target.startswith('windows') else 'ffmpeg')
    tool = tool.resolve()
    probe = tool.with_name('ffprobe.exe' if tool.suffix == '.exe' else 'ffprobe')

    def identities():
        values = {}
        for executable in (tool, probe):
            with executable.open('rb') as stream:
                values[executable.name] = hashlib.file_digest(stream, 'sha256').hexdigest()
            if values[executable.name] != manifest['files'].get('bin/' + executable.name):
                raise ValueError('Qualification executable changed from manifest: ' + executable.name)
        return values

    before = identities()
    def run(*flags):
        proc = subprocess.run([str(tool), '-hide_banner', *flags], text=True,
                              stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=30, check=True)
        return proc.stdout
    raw = {kind: run('-' + flag) for kind, flag in {
        'encoder': 'encoders', 'decoder': 'decoders', 'filter': 'filters',
        'demuxer': 'demuxers', 'muxer': 'muxers', 'protocol': 'protocols',
        'hwaccel': 'hwaccels', 'bsf': 'bsfs'}.items()}
    config, license_text = run('-buildconf'), run('-L')
    lock = json.loads((args.root / 'sources.lock.json').read_text())
    for flag in lock['requiredConfigure']:
        if flag not in config.split():
            raise ValueError('missing required configure flag: ' + flag)
    for flag in lock['prohibitedConfigure']:
        if flag in config.split():
            raise ValueError('prohibited configure flag: ' + flag)
    if not re.search(r'GPL version 3|GPLv3|GNU General Public License.*?version 3', license_text, re.S | re.I):
        raise ValueError('GPLv3 license required')
    inventories = {kind: entries(value, kind) for kind, value in raw.items()}
    inventories['library'] = set(re.findall(r'--enable-([a-z0-9_-]+)', config))
    contract = json.loads((args.root / 'requirements.v1.json').read_text())
    missing = missing_features(contract, TARGETS[target], inventories)
    # soxr must be selectable, not just a configured but unavailable dependency.
    resample = run('-h', 'filter=aresample')
    if not re.search(r'\bsoxr\b', resample):
        missing.append('audio.resampler:soxr')
    # This stock BSF strips or recompresses RPU data. It must never be
    # mistaken for the separately qualified profile-7-to-8.1 converter.
    stock_dovi = run('-h', 'bsf=dovi_rpu')
    stock_options = set(re.findall(r'^\s+-(\w+)\s', stock_dovi, re.M))
    if not {'strip', 'compression'} <= stock_options or 'mode' in stock_options:
        missing.append('metadata.stock-dovi-rpu:strip/compression-only')
    profile_dovi = run('-h', 'bsf=dovi_profile')
    if not re.search(r'(?m)^\s+-mode\s+<int>.*\(from 2 to 2\).*\(default 2\)', profile_dovi):
        missing.append('metadata.dovi-profile:mode-exactly-2')
    join_help = run('-h', 'demuxer=mpegts')
    join_default_off = bool(re.search(r'(?m)^\s+-merge_dovi\s+<boolean>.*\(default false\)', join_help))
    if not join_default_off:
        missing.append('metadata.dovi-join:default-off-mpegts-option')
    report = {'target': target, 'contractId': contract['contractId'], 'version': run('-version').splitlines()[0],
              'sourceCommit': lock['ffmpeg']['commit'], 'missing': missing,
              'configuration': config, 'binarySHA256': before,
              'MPEGTSMergeDolbyVisionDefaultFalse': join_default_off,
              'stockDoviRpuOptions': sorted(stock_options),
              'inventories': {k: sorted(v) for k, v in inventories.items()}}
    if target.startswith('macos-'):
        report['macosLibblurayUdfread'] = macos_udfread(tool, target, lock)
    if missing:
        if args.report:
            args.report.write_text(json.dumps(report, indent=2) + '\n')
        raise ValueError('missing target features: ' + ', '.join(missing))
    subprocess.run([str(probe), '-hide_banner', '-version'], check=True, stdout=subprocess.DEVNULL, timeout=10)
    if identities() != before:
        raise ValueError('Qualification executable pair changed during inventory')
    if args.report:
        args.report.write_text(json.dumps(report, indent=2) + '\n')
    print('Verified required features for ' + target)


if __name__ == '__main__':
    try:
        main()
    except (ValueError, subprocess.SubprocessError) as exc:
        sys.exit(str(exc))
