#!/usr/bin/env python3
"""Expose the selected Apple SDK's system iconv/libxml2, as Jellyfin does."""
import argparse
import hashlib
import json
from pathlib import Path
import platform
import re
import subprocess


def digest(path):
    with path.open('rb') as stream: return hashlib.file_digest(stream, 'sha256').hexdigest()


def prepare(prefix, receipt):
    if platform.system() != 'Darwin': raise ValueError('native Apple SDK required')
    sdk = Path(subprocess.check_output(['/usr/bin/xcrun', '--show-sdk-path'], text=True).strip()).resolve(strict=True)
    paths = dict(xmlHeader=sdk / 'usr/include/libxml2/libxml/xmlversion.h',
                 xmlLinkStub=sdk / 'usr/lib/libxml2.tbd',
                 iconvHeader=sdk / 'usr/include/iconv.h', iconvLinkStub=sdk / 'usr/lib/libiconv.tbd',
                 bzlibHeader=sdk / 'usr/include/bzlib.h', bzlibLinkStub=sdk / 'usr/lib/libbz2.tbd')
    for path in paths.values():
        if not path.resolve(strict=True).is_relative_to(sdk) or not path.is_file():
            raise ValueError('system library input escaped the selected SDK')
    match = re.search(r'#define\s+LIBXML_DOTTED_VERSION\s+"([0-9.]+)"', paths['xmlHeader'].read_text())
    if not match: raise ValueError('SDK libxml2 version is unavailable')
    directory = prefix / 'lib/pkgconfig'; directory.mkdir(parents=True, exist_ok=True)
    pc = directory / 'libxml-2.0.pc'
    pc.write_text('prefix=' + str(sdk / 'usr') + '\nlibdir=${prefix}/lib\nincludedir=${prefix}/include\n'
        'Name: libXML\nDescription: Selected Apple SDK system libxml2\nVersion: ' + match[1] + '\n'
        'Libs: -lxml2\nCflags: -I${includedir}/libxml2\n')
    record = dict(schemaVersion=1, sdk=str(sdk), libxml2Version=match[1], systemLibraryException=True,
        inputs={name: dict(path=str(path), sha256=digest(path)) for name, path in paths.items()},
        pkgconfigSHA256=digest(pc), qualified=False, release=False,
        scope='SDK link interfaces only; runtime uses macOS system libraries, no Homebrew media library')
    receipt.parent.mkdir(parents=True, exist_ok=True)
    receipt.write_text(json.dumps(record, indent=2) + '\n')
    return record


if __name__ == '__main__':
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--prefix', type=Path, required=True); p.add_argument('--receipt', type=Path, required=True)
    a = p.parse_args(); print(json.dumps(prepare(a.prefix, a.receipt)))
