#!/usr/bin/env python3
"""Collect full notices from the exact retained dependency source archives."""
import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import tarfile


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('dependencies', type=Path)
    args = parser.parse_args()
    work = args.dependencies
    records = json.loads((work / 'dependency-inputs.json').read_text())
    inventory = []
    for record in records:
        archive = work / 'sources' / record['sourceArchive']
        if archive.name != record['sourceArchive'] or digest(archive) != record['sourceArchiveSHA256']:
            raise ValueError('Dependency source changed during licence closure')
        stage = Path(record['script']).stem
        notices = dict(record['licenses'])
        # Preserve the first pass's embedded NVIDIA header notices as well.
        for relative, expected in notices.items():
            if relative.endswith('#opening-comment'):
                original = Path(relative.removesuffix('#opening-comment'))
                path = work / 'licenses' / stage / original.parent / ('LICENSE-' + original.name + '.txt')
            else:
                path = work / 'licenses' / stage / relative
            if digest(path) != expected:
                raise ValueError('Collected licence was modified: ' + str(path))
        with tarfile.open(archive, mode='r|xz') as source:
            for member in source:
                relative = PurePosixPath(member.name)
                if relative.is_absolute() or '..' in relative.parts:
                    raise ValueError('Unsafe dependency source path')
                if not member.isfile() or '.git' in relative.parts:
                    continue
                name = relative.name.lower()
                # FreeType's LICENSE refers to docs/FTL.TXT and GPLv2.TXT.
                # Opus/SVT also supply separate patent grants. These texts are
                # mandatory, even when they are not called LICENSE/COPYING.
                if not (name.startswith(('license', 'licence', 'copying', 'copyright', 'notice', 'patent', 'unlicense')) or
                        name in ('ftl.txt', 'gplv2.txt', 'gplv3.txt', 'lgplv21.txt', 'ofl.txt') or
                        any(p.lower() in ('license', 'licenses', 'licence', 'licences', 'licensing', 'legal') for p in relative.parts[:-1])):
                    continue
                if member.size > 2 << 20:
                    raise ValueError('Unexpectedly large licence text: ' + str(relative))
                raw = source.extractfile(member).read()
                destination = work / 'licenses' / stage / str(relative)
                destination.parent.mkdir(parents=True, exist_ok=True)
                destination.write_bytes(raw)
                notices[str(relative)] = hashlib.sha256(raw).hexdigest()
        if not notices:
            raise ValueError('Dependency licence inventory is empty: ' + stage)
        inventory.append({'script': record['script'], 'sourceArchiveSHA256': record['sourceArchiveSHA256'],
                          'notices': notices})
    destination = work / 'license-inventory.json'
    temporary = destination.with_suffix('.new')
    temporary.write_text(json.dumps(inventory, indent=2, sort_keys=True) + '\n')
    temporary.replace(destination)
    print('Verified full licence/patent inventory for ' + str(len(inventory)) + ' source dependencies')


if __name__ == '__main__':
    main()
