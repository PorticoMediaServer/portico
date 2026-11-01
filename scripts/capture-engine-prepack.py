#!/usr/bin/env python3
"""Preserve a failed Mac packing input, never a qualified engine."""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import stat
import tarfile
import tempfile


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def inventory(prefix):
    result = {}
    for path in sorted(prefix.rglob('*')):
        name = path.relative_to(prefix).as_posix()
        mode = path.lstat().st_mode
        if stat.S_ISLNK(mode):
            resolved = path.resolve(strict=True)
            if not resolved.is_relative_to(prefix.resolve()) or not resolved.is_file():
                raise ValueError('Only internal file symlinks may be captured: ' + name)
            result[name] = {'link': os.readlink(path)}
        elif stat.S_ISREG(mode):
            result[name] = {'sha256': digest(path), 'bytes': path.stat().st_size,
                            'mode': stat.S_IMODE(mode)}
        elif not stat.S_ISDIR(mode):
            raise ValueError('Special prefix entry: ' + name)
    if len(result) > 200000:
        raise ValueError('Prefix file count exceeds capture bound')
    return result


def capture(target, build, output, provenance, root):
    if target not in ('macos-arm64', 'macos-x64'):
        return None
    prefix = build / 'recipe/workspace'
    source = build / 'corresponding-source.tar.xz'
    if not source.is_file() or not all((prefix / 'bin' / name).is_file()
            and (prefix / 'bin' / name).stat().st_size for name in ('ffmpeg', 'ffprobe', 'portico-disc')):
        return None  # Compilation/source preparation has not completed.
    if prefix.is_symlink() or source.is_symlink():
        raise ValueError('Capture roots must be regular owned inputs')
    spec = importlib.util.spec_from_file_location('assemble', root / 'scripts/assemble-engine.py')
    assemble = importlib.util.module_from_spec(spec); spec.loader.exec_module(assemble)
    configuration = assemble.configuration(prefix / 'bin/ffmpeg')
    before = inventory(prefix)
    inputs = {'corresponding-source.tar.xz': source}
    inputs.update({name: root / 'third_party/ffmpeg' / name for name in
                   ('NOTICE.md', 'sources.lock.json', 'requirements.v1.json')})
    hashes = {name: digest(path) for name, path in inputs.items()}
    total = sum(item.get('bytes', 0) for item in before.values()) + sum(p.stat().st_size for p in inputs.values())
    if total > 6 * 1024**3 or shutil.disk_usage(output.parent).free < total + 1024**3:
        raise ValueError('Capture size/free-space bound exceeded')
    if output.exists():
        raise ValueError('Fresh capture destination required')
    temporary = Path(tempfile.mkdtemp(prefix='.prepack-', dir=output.parent))
    try:
        archive = temporary / 'payload.tar'
        def relative_links(info):
            if info.issym():
                original = prefix / Path(info.name).relative_to('prefix')
                info.linkname = os.path.relpath(original.resolve(strict=True), original.parent)
            return info
        with tarfile.open(archive, 'w', dereference=False) as stream:
            stream.add(prefix, arcname='prefix', filter=relative_links)
            for name, path in inputs.items():
                stream.add(path, arcname=name)
        if before != inventory(prefix) or hashes != {name: digest(path) for name, path in inputs.items()}:
            raise ValueError('Packing input changed during capture')
        receipt = dict(provenance)
        receipt.update(schemaVersion=1, target=target,
            qualified=False, release=False, readyForPublication=False, nativeEngineChecks=False,
            originalPrefix=str(prefix.resolve()), files=before, inputs=hashes,
            configuration=configuration, payloadSHA256=digest(archive), payloadBytes=archive.stat().st_size,
            purpose='NOT-FOR-USE: failed packing recovery input; not an engine or qualification')
        (temporary / 'receipt.json').write_text(json.dumps(receipt, indent=2) + '\n')
        (temporary / 'NOT-FOR-USE.txt').write_text(receipt['purpose'] + '\n')
        temporary.rename(output)
        return {'payloadSHA256': receipt['payloadSHA256'], 'payloadBytes': receipt['payloadBytes']}
    finally:
        if temporary.exists():
            shutil.rmtree(temporary)
