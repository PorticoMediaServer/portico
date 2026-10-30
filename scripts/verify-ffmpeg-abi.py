#!/usr/bin/env python3
"""Check Linux executable ABI and dynamic closure without executing media."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('bundle', type=Path)
    parser.add_argument('--report', type=Path, required=True)
    args = parser.parse_args()
    manifest = json.loads((args.bundle / 'toolchain-manifest.json').read_text())
    architectures = {
        'linux-x64': ('Advanced Micro Devices X86-64', '/lib64/ld-linux-x86-64.so.2'),
        'linux-arm64': ('AArch64', '/lib/ld-linux-aarch64.so.1'),
    }
    if manifest['target'] not in architectures:
        raise ValueError('This verifier qualifies Linux ELF targets only')
    machine, interpreter = architectures[manifest['target']]
    policy = json.loads((args.bundle / 'sources.lock.json').read_text())['linuxSourceBuild']['abi']
    ceiling = tuple(int(n) for n in policy['maximumGLIBCVersion'].split('.'))
    results = {}
    for name in ('ffmpeg', 'ffprobe', 'portico-disc'):
        path = args.bundle / 'bin' / name
        before = digest(path)
        if before != manifest['files'].get('bin/' + name):
            raise ValueError('ELF executable changed from bundle manifest: ' + name)

        def readelf(flag):
            process = subprocess.run(['readelf', flag, '--wide', str(path)], check=True,
                                     stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=20)
            if len(process.stdout) > 4 << 20 or len(process.stderr) > 1 << 20:
                raise ValueError('ELF metadata exceeded its output bound')
            return process.stdout

        header, program = readelf('--file-header'), readelf('--program-headers')
        dynamic, versions = readelf('--dynamic'), readelf('--version-info')
        if (not re.search(r'Class:\s+ELF64\b', header) or
                not re.search(r'Data:.*little endian', header) or
                not re.search(r'Machine:\s+' + re.escape(machine) + r'\s*$', header, re.M) or
                '[Requesting program interpreter: ' + interpreter + ']' not in program):
            raise ValueError('ELF architecture/interpreter differs from target: ' + name)
        needed = re.findall(r'\(NEEDED\).*?\[(.*?)\]', dynamic)
        if not needed or set(needed) - set(policy['allowedELFDependencies']):
            raise ValueError('Unexpected dynamically linked media/runtime dependency: ' + name)
        if re.search(r'\((?:RPATH|RUNPATH)\)', dynamic):
            raise ValueError('Unexpected executable runtime search path: ' + name)
        tags = sorted(set(re.findall(r'Name: GLIBC_([0-9.]+)', versions)),
                      key=lambda value: tuple(int(n) for n in value.split('.')))
        if not tags or tuple(int(n) for n in tags[-1].split('.')) > ceiling or 'GLIBC_PRIVATE' in versions:
            raise ValueError('Executable exceeds the target public GLIBC ABI: ' + name)
        if digest(path) != before:
            raise ValueError('Executable changed during static ELF qualification: ' + name)
        results[name] = {'sha256': before, 'needed': needed, 'maximumGLIBCVersion': tags[-1],
                         'machine': machine, 'interpreter': interpreter}
    args.report.write_text(json.dumps({'target': manifest['target'], 'executables': results,
        'boundary': 'Static ELF ABI/closure only; no device, decode, driver or playback acceptance.'}, indent=2) + '\n')
    print('Verified source-built Linux executable ABI and dynamic closure')


if __name__ == '__main__':
    main()
