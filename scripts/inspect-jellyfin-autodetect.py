#!/usr/bin/env python3
"""Read official upstream inventories for comparison only; never engine intake."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import tarfile
import urllib.request
import zipfile

FIXTURES = {
    'linux-x64': ('linux64-gpl.tar.xz', 'b86dc023c64a5a9a410e6e3a938a970460214fae78f31c1553c9f88f1c289b6a'),
    'linux-arm64': ('linuxarm64-gpl.tar.xz', '7bc8e8d0986f7f4693f63e9728570f671f07b6e21c05d5465dd7ce461d1631dc'),
    'windows-x64': ('win64-clang-gpl.zip', '3691f8b2d4c39c511b5faff1c8c4687bd782dac823e63ba69f0755f7ce1af9ce'),
    'macos-arm64': ('macarm64-gpl.tar.xz', '22445d7299742749ad2eeb9ce87963d50def0357e45b3e6c7b69987c8365dbf6'),
    'macos-x64': ('mac64-gpl.tar.xz', 'cb2b5c154d49a6b6bfe29fa6c16510e85dcbf60b805764121a167b093d4bb6fb'),
}


def inspect(target, work):
    work.mkdir(parents=True, exist_ok=False)
    name, expected = FIXTURES[target]
    name = 'jellyfin-ffmpeg_8.1.3-1_portable_' + name
    url = 'https://github.com/jellyfin/jellyfin-ffmpeg/releases/download/v8.1.3-1/' + name
    archive = work / name
    with urllib.request.urlopen(url, timeout=60) as response, archive.open('wb') as output:
        count = 0
        while chunk := response.read(1024**2):
            count += len(chunk)
            if count > 128 * 1024**2: raise ValueError('comparison archive exceeded bound')
            output.write(chunk)
    with archive.open('rb') as stream:
        if hashlib.file_digest(stream, 'sha256').hexdigest() != expected:
            raise ValueError('official comparison artifact changed')
    payload = work / 'payload'; payload.mkdir()
    if name.endswith('.zip'):
        with zipfile.ZipFile(archive) as source:
            for member in source.infolist():
                path = Path(member.filename)
                if path.is_absolute() or '..' in path.parts: raise ValueError('unsafe comparison archive path')
            source.extractall(payload)
    else:
        with tarfile.open(archive) as source: source.extractall(payload, filter='data')
    tools = list(payload.rglob('ffmpeg.exe' if target.startswith('windows') else 'ffmpeg'))
    if len(tools) != 1: raise ValueError('one exact upstream comparison executable required')
    binary = tools[0]
    reports = {}
    for name in ('buildconf', 'devices', 'hwaccels', 'encoders', 'filters', 'protocols'):
        reports[name] = subprocess.check_output([binary, '-hide_banner', '-' + name],
            stderr=subprocess.STDOUT, text=True, timeout=30)
    if target.startswith('macos'):
        reports['linkedLibraries'] = subprocess.check_output(['/usr/bin/otool', '-L', binary], text=True)
        # Record actual selected SDK system library inputs without compiling.
        import importlib.util
        spec = importlib.util.spec_from_file_location('sdk_libraries', Path(__file__).with_name('prepare-macos-system-libs.py'))
        module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
        module.prepare(work / 'sdk-metadata', work / 'sdk-inputs.json')
    record = dict(schemaVersion=1, comparisonOnly=True, engineIntake=False, qualified=False, release=False,
                  target=target, upstreamVersion='8.1.3-1', url=url, archiveSHA256=expected,
                  executableSHA256=hashlib.sha256(binary.read_bytes()).hexdigest(), reports=reports)
    (work / 'comparison.json').write_text(json.dumps(record, indent=2) + '\n')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--target', choices=FIXTURES, required=True)
    parser.add_argument('--work', type=Path, required=True)
    args = parser.parse_args(); inspect(args.target, args.work)
