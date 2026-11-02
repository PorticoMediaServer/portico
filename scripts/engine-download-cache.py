#!/usr/bin/env python3
"""Persist exact BtbN source snapshots; cache hints never qualify an engine."""
import argparse
import hashlib
import json
import lzma
import os
from pathlib import Path
import re
import shutil

LIMIT = 20 * 1024**3
NAME = re.compile(r'[A-Za-z0-9][A-Za-z0-9_-]{0,160}_[a-f0-9]{64}\.tar\.xz')


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def sync(action, recipe, store):
    if os.environ.get('PORTICO_ENGINE_BUILD_CONTEXT') != 'development':
        raise ValueError('source snapshot cache requires explicit development context')
    directory = recipe / '.cache/downloads'
    for path in (recipe, store, directory):
        if path.is_symlink():
            raise ValueError('source snapshot cache root cannot be a link')
    store.mkdir(parents=True, exist_ok=True); directory.mkdir(parents=True, exist_ok=True)
    receipt = store / 'receipt.json'
    if receipt.is_symlink(): raise ValueError('source snapshot inventory cannot be a link')
    entries = json.loads(receipt.read_text()) if receipt.exists() else {}
    if not isinstance(entries, dict) or len(entries) > 1000:
        raise ValueError('bounded source snapshot inventory required')
    total, hits = 0, []
    for name, sha in entries.items():
        path = store / name
        if (not NAME.fullmatch(name) or not re.fullmatch('[a-f0-9]{64}', str(sha)) or
                path.is_symlink() or not path.is_file() or digest(path) != sha):
            raise ValueError('cached source snapshot changed: ' + name)
        total += path.stat().st_size
        if total > LIMIT: raise ValueError('source snapshot cache exceeds 20 GiB')
        if action == 'restore':
            destination = directory / name
            if destination.is_symlink(): raise ValueError('source snapshot destination is a link')
            if destination.exists() and digest(destination) != sha:
                raise ValueError('source snapshot differs from retained exact bytes')
            if not destination.exists(): shutil.copyfile(path, destination)
            hits.append(name)
    if action == 'save':
        for path in sorted(directory.iterdir()):
            # Incomplete downloads and unrelated files never enter the cache.
            if not NAME.fullmatch(path.name): continue
            if path.is_symlink() or not path.is_file(): raise ValueError('linked source snapshot')
            # A killed tar/xz writer can leave a command-named partial file.
            # Never turn that into a persistent hit on the next attempt.
            try:
                with lzma.open(path, 'rb') as stream:
                    while stream.read(1024**2): pass
            except (lzma.LZMAError, EOFError):
                continue
            sha = digest(path); destination = store / path.name
            if path.name in entries:
                if entries[path.name] != sha: raise ValueError('source snapshot bytes changed for an existing command hash')
                continue
            total += path.stat().st_size
            if total > LIMIT or len(entries) >= 1000: raise ValueError('source snapshot cache bound exceeded')
            pending = store / (path.name + '.new'); shutil.copyfile(path, pending)
            if digest(pending) != sha: raise ValueError('source snapshot changed during copy')
            pending.replace(destination); entries[path.name] = sha
        pending = receipt.with_suffix('.new'); pending.write_text(json.dumps(entries, sort_keys=True) + '\n'); pending.replace(receipt)
    report = dict(action=action, restored=len(hits), snapshots=len(entries), bytes=total,
                  qualified=False, release=False)
    (recipe.parent / ('source-cache-' + action + '.json')).write_text(json.dumps(report, indent=2) + '\n')
    return report


if __name__ == '__main__':
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('action', choices=('restore', 'save')); p.add_argument('--recipe', type=Path, required=True)
    p.add_argument('--store', type=Path, default=os.environ.get('PORTICO_ENGINE_DOWNLOAD_CACHE'))
    a = p.parse_args()
    if not a.store: p.error('explicit source snapshot store required')
    print(json.dumps(sync(a.action, a.recipe, a.store)))
