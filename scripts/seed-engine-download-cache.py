#!/usr/bin/env python3
"""Seed source bytes from an exact development artifact, never reuse binaries."""
import argparse
from datetime import datetime, timedelta, timezone
import json
import os
from pathlib import Path, PurePosixPath
import runpy
import re
import shutil
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]
CI = runpy.run_path(str(ROOT / 'scripts/ci-ffmpeg-toolchain.py'))
CACHE = runpy.run_path(str(ROOT / 'scripts/engine-download-cache.py'))


def source_pin(target):
    pin = json.loads(json.dumps(CI['WINDOWS_NATIVE_RETRY']))
    if target == 'linux-arm64':
        pin.update(target=target,
            name='engine-cold-linux-arm64-d0ec939ff4f4e2b06b354dde0340c37827578b36-NOT-FOR-USE',
            engineSourceSHA='d0ec939ff4f4e2b06b354dde0340c37827578b36',
            archiveSHA256='f6a7a09f550166b53d529d3fb42e998223c2f8d622138fa9f855a54753df957c',
            createdAt='2026-11-01T16:50:35Z', expiresAt='2026-11-15T16:50:20Z',
            origin=dict(kind='github-actions', repository='PorticoMediaServer/portico',
                        runId=41317199099, runAttempt=1, artifactId=13092526098))
    elif target != 'windows-x64': raise ValueError('only explicit BtbN source seeds are supported')
    created = datetime.fromisoformat(pin['createdAt'].replace('Z', '+00:00'))
    expires = datetime.fromisoformat(pin['expiresAt'].replace('Z', '+00:00'))
    if not created <= datetime.now(timezone.utc) < expires or not timedelta(0) < expires - created <= timedelta(days=14):
        raise ValueError('development source seed expired or outside its 14-day window')
    return pin


def snapshots(source, destination):
    directory = destination / '.cache/downloads'; directory.mkdir(parents=True)
    total, seen, count, aliases = 0, set(), 0, []
    with tarfile.open(source, 'r|xz') as archive:
        for item in archive:
            count += 1
            if count > 500000: raise ValueError('source inventory exceeds seed bound')
            parts = PurePosixPath(item.name).parts
            if parts[:3] != ('recipe', '.cache', 'downloads'): continue
            if len(parts) == 3 and item.isdir(): continue
            if len(parts) == 4 and item.issym():
                # BtbN keeps a plain stage-name alias beside its exact command
                # snapshot. Validate it, but let the pinned downloader recreate it.
                alias = re.fullmatch(r'([A-Za-z0-9][A-Za-z0-9_-]{0,160})\.tar\.xz', parts[-1])
                if (not alias or not re.fullmatch(re.escape(alias[1]) + r'_[a-f0-9]{64}\.tar\.xz', item.linkname)):
                    raise ValueError('unsafe source snapshot alias: ' + item.name)
                aliases.append(item.linkname)
                continue
            if (len(parts) != 4 or not CACHE['NAME'].fullmatch(parts[-1]) or
                    not item.isfile() or item.name in seen or not 0 < item.size <= CACHE['LIMIT']):
                raise ValueError('unsafe or duplicate source snapshot seed member: ' + item.name)
            seen.add(item.name); total += item.size
            if len(seen) > 1000 or total > CACHE['LIMIT']: raise ValueError('source snapshot seed exceeds bound')
            path = directory / parts[-1]
            with archive.extractfile(item) as inp, path.open('xb') as out: shutil.copyfileobj(inp, out, 1024**2)
            if path.stat().st_size != item.size: raise ValueError('truncated source snapshot seed')
    if any('recipe/.cache/downloads/' + name not in seen for name in aliases):
        raise ValueError('source snapshot alias has no checked command snapshot')
    if not seen: raise ValueError('retained source contains no dependency snapshots')
    return len(seen)


def seed(target, store, work):
    if os.environ.get('PORTICO_ENGINE_BUILD_CONTEXT') != 'development':
        raise ValueError('source seeding is development-only')
    provenance = CI['context']()  # Real hosted source/workflow, never synthesized.
    work.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='source-seed-', dir=work) as temporary:
        temp = Path(temporary)
        if (store / 'receipt.json').exists():
            report = CACHE['sync']('restore', temp / 'validated', store)
            report.update(kind='existing-development-source-cache', qualifier=provenance)
        else:
            pin = source_pin(target)
            intake = CI['module']('fetch-engine')
            raw = temp / 'artifact.zip'; intake.remote(pin, raw)
            if CACHE['digest'](raw) != pin['archiveSHA256']: raise ValueError('source seed artifact changed')
            intake.extract(raw, temp / 'transfer')
            transfer = temp / 'transfer'
            sender = json.loads((transfer / 'transfer.json').read_text())
            producer = CI['check_retry_transfer'](sender, pin, CACHE['digest'](transfer / 'bundle.tar'))
            intake.extract(transfer / 'bundle.tar', temp / 'expanded')
            bundle = temp / 'expanded/bundle'; source = bundle / 'corresponding-source.tar.xz'
            if (CACHE['digest'](bundle / 'toolchain-manifest.json') != sender['toolchainManifestSHA256'] or
                    CACHE['digest'](source) != sender['correspondingSourceSHA256']):
                raise ValueError('source seed differs from immutable producer transfer')
            count = snapshots(source, temp / 'sources')
            report = CACHE['sync']('save', temp / 'sources', store)
            report.update(kind='development-source-snapshot-seed', inputProducer=producer, inputArtifact=pin,
                          sourceArchiveSHA256=sender['correspondingSourceSHA256'], seeded=count,
                          qualifier=provenance, reusedBinaries=False)
    report.update(qualified=False, release=False, readyForPublication=False)
    (work / 'source-cache-seed.json').write_text(json.dumps(report, indent=2) + '\n')
    return report


if __name__ == '__main__':
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--target', choices=('windows-x64', 'linux-arm64'), required=True)
    p.add_argument('--store', type=Path, required=True); p.add_argument('--work', type=Path, required=True)
    a = p.parse_args(); print(json.dumps(seed(a.target, a.store, a.work)))
