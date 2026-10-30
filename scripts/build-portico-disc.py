#!/usr/bin/env python3
"""Link the checked disc reader with the FFmpeg job's exact static libraries."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shlex
import subprocess


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source', required=True, type=Path)
    parser.add_argument('--source-commit', required=True)
    parser.add_argument('--prefix', required=True, type=Path)
    parser.add_argument('--output', required=True, type=Path)
    parser.add_argument('--metadata', required=True, type=Path)
    parser.add_argument('--target', required=True)
    args = parser.parse_args()
    prefix = args.prefix.resolve(strict=True)
    source_files = {'portico-disc.c': digest(args.source)}
    if '#include "mpegts_dovi.h"' in args.source.read_text():
        header = args.source.parent / 'mpegts_dovi.h'
        if header.is_symlink() or not header.is_file():
            raise ValueError('checked disc source requires its exact PSI header beside it')
        source_files['mpegts_dovi.h'] = digest(header)
    env = dict(os.environ, PKG_CONFIG_PATH='',
               PKG_CONFIG_LIBDIR=os.pathsep.join(str(prefix / p) for p in ('lib/pkgconfig', 'share/pkgconfig')))
    pkg_config = shlex.split(env.get('PKG_CONFIG', 'pkg-config'))

    def pkg(*flags):
        return subprocess.check_output(pkg_config + list(flags), env=env, text=True).strip()

    modules = {'libbluray': 'bluray', 'dvdnav': 'dvdnav', 'dvdread': 'dvdread', 'libudfread': 'udfread'}
    archives = {}
    provenance = {}
    for module, library in modules.items():
        archive = Path(pkg('--variable=libdir', module)) / ('lib' + library + '.a')
        archive = archive.resolve(strict=True)
        if not archive.is_relative_to(prefix) or not archive.is_file():
            raise ValueError('disc library must be a static archive in the FFmpeg prefix: ' + module)
        archives['-l' + library] = str(archive)
        provenance[module] = {'version': pkg('--modversion', module),
                              'archive': str(archive.relative_to(prefix)), 'sha256': digest(archive)}
    bluray_links = shlex.split(pkg('--static', '--libs', 'libbluray'))
    if '-ludfread' not in bluray_links and archives['-ludfread'] not in bluray_links:
        raise ValueError('libbluray must use its UDF reader, not only folder input')
    links = shlex.split(pkg('--static', '--libs', *modules))
    if any(name in flag.lower() for flag in links for name in ('dvdcss', 'libaacs', '-laacs', 'bdplus')):
        raise ValueError('decryption libraries are not shipped')
    # An absolute .a defeats shared-library preference even if a builder happens
    # to contain both library types. Dependencies come from the same private pc files.
    links = [archives.get(flag, flag) for flag in links]
    compiler = shlex.split(env.get('CC', 'cc'))
    command = (compiler + shlex.split(env.get('CFLAGS', '')) +
               ['-std=c11', '-O2', '-Wall', '-Wextra', '-Werror', '-D_FILE_OFFSET_BITS=64'] +
               shlex.split(pkg('--cflags', *modules)) + [str(args.source), '-o', str(args.output)] +
               shlex.split(env.get('LDFLAGS', '')) + links + shlex.split(env.get('LIBS', '')))
    if any(name in flag.lower() for flag in command for name in ('dvdcss', 'libaacs', '-laacs', 'bdplus')):
        raise ValueError('decryption libraries in the link command')
    args.output.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run(command, check=True, env=env)
    metadata = {'schema': 1, 'tool': 'portico-disc', 'target': args.target,
                'sourceCommit': args.source_commit, 'sourceSha256': digest(args.source),
                'sourceFiles': source_files,
                'binarySha256': digest(args.output), 'staticLibraries': provenance,
                'decryption': False}
    args.metadata.parent.mkdir(parents=True, exist_ok=True)
    args.metadata.write_text(json.dumps(metadata, indent=2, sort_keys=True) + '\n')


if __name__ == '__main__':
    main()
