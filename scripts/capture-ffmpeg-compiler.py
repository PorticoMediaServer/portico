#!/usr/bin/env python3
"""Retain the immutable compiler's linked runtime and import-wrapper notices."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--lock', required=True, type=Path)
    parser.add_argument('--output', required=True, type=Path)
    args = parser.parse_args()
    lock = json.loads(args.lock.read_text())['linuxSourceBuild']
    inputs = lock['compilerLinkedInputs']
    output = args.output
    notices = output / 'LICENSES/compiler'
    notices.mkdir(parents=True)
    for name, entry in inputs['notices'].items():
        source = Path(entry['path'])
        if digest(source) != entry['sha256']:
            raise ValueError('Compiler input notice changed: ' + name)
        shutil.copyfile(source, notices / name)
    implib = Path('/opt/implib')
    commit = subprocess.check_output(['git', '-C', str(implib), 'rev-parse', 'HEAD'], text=True).strip()
    if commit != inputs['implibCommit']:
        raise ValueError('Import-wrapper source identity changed')
    subprocess.run(['git', '-C', str(implib), 'diff', '--quiet', 'HEAD', '--'], check=True)
    sources = output / 'compiler-source/Implib.so'
    # Only tracked source is an input; Python caches from running the generator
    # are never corresponding source and carry build-specific timestamps.
    tracked = subprocess.check_output(['git', '-C', str(implib), 'ls-files', '-z']).decode().split('\0')
    for name in filter(None, tracked):
        source = implib / name
        if source.is_symlink() or not source.is_file() or not source.resolve().is_relative_to(implib):
            raise ValueError('Import-wrapper source escaped the source tree: ' + name)
        destination = sources / name
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(source, destination)
    runtimes = {}
    for compiler, archive in [('CC', 'libgcc.a'), ('CC', 'libgcc_eh.a'),
                              ('CC', 'libgomp.a'), ('CXX', 'libstdc++.a')]:
        command = shlex.split(os.environ[compiler])
        version = subprocess.check_output(command + ['-dumpfullversion'], text=True).strip()
        if version != inputs['gccVersion']:
            raise ValueError('Compiler runtime version changed: ' + version)
        path = Path(subprocess.check_output(command + ['-print-file-name=' + archive], text=True).strip())
        if not path.is_absolute() or not path.resolve().is_relative_to('/opt/ct-ng'):
            raise ValueError('Compiler runtime escaped the immutable toolchain')
        runtimes[archive] = {'compilerVersion': version, 'sha256': digest(path)}
    config = Path('/opt/ct-ng/share/x86_64-ffbuild-linux-gnu-ct-ng.config.bz2')
    if digest(config) != inputs['configurationSHA256']:
        raise ValueError('Compiler build configuration changed')
    shutil.copyfile(config, output / 'compiler-source/crosstool-ng.config.bz2')
    tools = {name: subprocess.check_output(shlex.split(os.environ[name]) + ['--version'], text=True).splitlines()[0]
             for name in ('CC', 'CXX', 'LD', 'AR', 'RANLIB', 'NM')}
    tools['clang'] = subprocess.check_output(['clang', '--version'], text=True).splitlines()[0]
    record = {'compilerImage': lock['compilerImage'], 'implibCommit': commit,
              'toolVersions': tools,
              'runtimeArchives': runtimes, 'notices': inputs['notices'],
              'configurationSHA256': digest(config),
              'importWrapperSources': {str(p.relative_to(sources)): digest(p)
                                       for p in sorted(sources.rglob('*')) if p.is_file()}}
    (output / 'compiler-linked-inputs.json').write_text(json.dumps(record, indent=2, sort_keys=True) + '\n')


if __name__ == '__main__':
    main()
