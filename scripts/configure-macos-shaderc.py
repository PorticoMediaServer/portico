#!/usr/bin/env python3
"""Select the existing combined static shaderc package for the native Mac link."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import subprocess


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def select_static(prefix, receipt):
    prefix = prefix.resolve(strict=True)
    directory = prefix / 'lib/pkgconfig'
    combined = directory / 'shaderc_combined.pc'
    selected = directory / 'shaderc.pc'
    archive = prefix / 'lib/libshaderc_combined.a'
    for path in (combined, selected, archive):
        if path.is_symlink() or not path.resolve(strict=True).is_relative_to(prefix):
            raise ValueError('Shaderc static-link input escaped the owned prefix')
    with archive.open('rb') as stream:
        if stream.read(8) != b'!<arch>\n':
            raise ValueError('The real combined shaderc static archive is required')
    original = digest(selected)
    text = combined.read_text()
    libs = re.findall(r'(?m)^Libs:[ \t]*(.*)$', text)
    if len(libs) != 1 or '-lshaderc_combined' not in shlex.split(libs[0]) or '-lshaderc_shared' in text:
        raise ValueError('Expected the source-built combined static shaderc module')
    private = re.findall(r'(?m)^Libs\.private:[ \t]*(.*)$', text)
    if len(private) > 1:
        raise ValueError('Ambiguous combined shaderc private link metadata')
    value = private[0] if private else ''
    if '-lc++' not in shlex.split(value):
        value = (value + ' -lc++').strip()
    if private:
        text = re.sub(r'(?m)^Libs\.private:.*$', 'Libs.private: ' + value, text)
    else:
        text = text.rstrip() + '\nLibs.private: ' + value + '\n'
    pending = selected.with_suffix('.pc.new')
    with pending.open('x') as out:
        out.write(text)
    pending.replace(selected)
    env = dict(os.environ, PKG_CONFIG_PATH=str(directory), PKG_CONFIG_LIBDIR=str(directory))
    env.pop('PKG_CONFIG_SYSROOT_DIR', None)
    flags = subprocess.check_output(['pkg-config', '--libs', '--static', 'shaderc'], env=env, text=True).strip()
    actual = shlex.split(flags)
    if '-lshaderc_combined' not in actual or '-lc++' not in actual or '-lshaderc_shared' in actual:
        raise ValueError('pkg-config did not select combined static shaderc and system libc++')
    record = {'originalShadercPCSHA256': original, 'combinedPCSHA256': digest(combined),
              'staticArchiveSHA256': digest(archive), 'selectedPCSHA256': digest(selected),
              'actualStaticLinkFlags': flags, 'runtimeAdded': False,
              'scope': 'Existing source-built shaderc combined archive; final native Mach-O closure still required'}
    receipt.parent.mkdir(parents=True, exist_ok=True)
    receipt.write_text(json.dumps(record, indent=2) + '\n')
    return record


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--prefix', type=Path, required=True)
    parser.add_argument('--receipt', type=Path, required=True)
    args = parser.parse_args()
    print(json.dumps(select_static(args.prefix, args.receipt)))
