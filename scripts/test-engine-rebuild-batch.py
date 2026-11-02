#!/usr/bin/env python3
"""Regression controls for exact source reuse, replay and four-target planning."""
import hashlib
import io
import lzma
import tarfile
import json
import os
from pathlib import Path
import runpy
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
CACHE = runpy.run_path(str(ROOT / 'scripts/engine-download-cache.py'))
PREFIX = runpy.run_path(str(ROOT / 'scripts/engine-prefix-cache.py'))
CI = runpy.run_path(str(ROOT / 'scripts/ci-ffmpeg-toolchain.py'))


class RebuildControls(unittest.TestCase):
    def test_source_snapshot_reuses_exact_tar_bytes_across_fresh_recipes_and_refuses_corruption(self):
        with tempfile.TemporaryDirectory() as temporary, patch.dict(os.environ, PORTICO_ENGINE_BUILD_CONTEXT='development'):
            root = Path(temporary); store = root / 'store'; first = root / 'first'; first.mkdir()
            downloads = first / '.cache/downloads'; downloads.mkdir(parents=True)
            name = '50-library_' + hashlib.sha256(b'locked source command').hexdigest() + '.tar.xz'
            snapshot = downloads / name; snapshot.write_bytes(lzma.compress(b'exact previously verified snapshot including original timestamps'))
            sha = CACHE['digest'](snapshot)
            partial = downloads / ('50-partial_' + 'b' * 64 + '.tar.xz')
            partial.write_bytes(b'interrupted xz stream')
            CACHE['sync']('save', first, store)
            self.assertFalse((store / partial.name).exists())
            fresh = root / 'fresh'; fresh.mkdir()
            report = CACHE['sync']('restore', fresh, store)
            self.assertEqual(report['restored'], 1)
            self.assertEqual(CACHE['digest'](fresh / '.cache/downloads' / name), sha)
            (store / name).write_bytes(b'changed')
            with self.assertRaisesRegex(ValueError, 'changed'):
                CACHE['sync']('restore', fresh, store)
        for context in ('release', 'qualified', ''):
            with patch.dict(os.environ, PORTICO_ENGINE_BUILD_CONTEXT=context), self.assertRaises(ValueError):
                CACHE['sync']('restore', Path('/must-not-exist'), Path('/must-not-exist'))

    def test_retained_source_seed_extracts_only_command_named_regular_snapshots(self):
        seed = runpy.run_path(str(ROOT / 'scripts/seed-engine-download-cache.py'))
        windows = seed['source_pin']('windows-x64'); arm = seed['source_pin']('linux-arm64')
        self.assertNotEqual(windows['archiveSHA256'], arm['archiveSHA256'])
        self.assertEqual(arm['origin']['artifactId'], 13092526098)
        self.assertEqual(arm['target'], 'linux-arm64')
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            name = 'recipe/.cache/downloads/50-library_' + 'a' * 64 + '.tar.xz'
            for mode in ('valid', 'link', 'duplicate', 'escape'):
                source = root / (mode + '.tar.xz')
                with tarfile.open(source, 'w:xz') as out:
                    member = tarfile.TarInfo(name if mode != 'escape' else 'recipe/.cache/downloads/../escape')
                    if mode == 'link': member.type = tarfile.SYMTYPE; member.linkname = '/outside'
                    else: member.size = 7
                    out.addfile(member, None if mode == 'link' else io.BytesIO(b'snapshot'[:7]))
                    if mode == 'duplicate': out.addfile(member, io.BytesIO(b'snapshot'[:7]))
                    ignored = tarfile.TarInfo('recipe/bin/never-execute'); ignored.size = 3
                    out.addfile(ignored, io.BytesIO(b'bin'))
                if mode == 'valid':
                    self.assertEqual(seed['snapshots'](source, root / mode), 1)
                    self.assertFalse((root / mode / 'bin').exists())
                else:
                    with self.assertRaises(ValueError): seed['snapshots'](source, root / mode)

    def test_native_rerun_keeps_original_attempt_and_rejects_future_cross_run_cross_source(self):
        qualifier = dict(engineSourceSHA='a' * 40, runId=17, runAttempt=2, context='github-actions')
        sender = dict(qualifier, runAttempt=1, target='windows-x64', constructionPassed=True, qualified=False, release=False)
        producer = CI['check_native_transfer'](sender, 'windows-x64', qualifier)
        self.assertEqual(producer['runAttempt'], 1)
        self.assertEqual(qualifier['runAttempt'], 2)
        for mutation in (dict(runAttempt=3), dict(runAttempt=True), dict(runId=18), dict(engineSourceSHA='b' * 40), dict(release=True)):
            with self.subTest(mutation=mutation), self.assertRaises(ValueError):
                CI['check_native_transfer'](dict(sender, **mutation), 'windows-x64', qualifier)

    def test_terminal_source_markers_and_outputs_never_enter_dependency_inventory(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            for name in ('packages/FFmpeg-release-8.1.tar.gz', 'packages/portico-ffmpeg-source-aa.tar.xz',
                         'packages/ffmpeg.done', 'packages/ffmpeg/libavfilter/stale.o',
                         'prefix/bin/ffmpeg', 'prefix/include/libavcodec/avcodec.h', 'prefix/lib/libavcodec.a'):
                path = root / name; path.parent.mkdir(parents=True, exist_ok=True); path.write_bytes(b'final input/output')
            keep = root / 'prefix/lib/libass.a'; keep.write_bytes(b'dependency')
            inventory = PREFIX['inventory']({name: root / name for name in ('packages', 'prefix')})
            self.assertIn('prefix/lib/libass.a', inventory)
            self.assertFalse(any(name in inventory for name in ('packages/ffmpeg.done', 'prefix/bin/ffmpeg', 'prefix/lib/libavcodec.a')))
            self.assertFalse(any('FFmpeg-release-' in name or 'portico-ffmpeg-source-' in name for name in inventory))

    def test_only_four_supported_targets_and_source_cache_always_saves(self):
        self.assertEqual(set(CI['TARGETS']), {'linux-x64', 'linux-arm64', 'windows-x64', 'macos-arm64'})
        workflow = (ROOT / '.github/workflows/ffmpeg-toolchain.yml').read_text()
        self.assertIn('if: always() && (matrix.target', workflow)
        self.assertIn('engine-download-cache', workflow)
        self.assertIn('timeout=300 * 60', (ROOT / 'scripts/ci-ffmpeg-toolchain.py').read_text())
        for path in (ROOT / '.github/workflows').glob('*.yml'):
            self.assertNotIn('macos-15-intel', path.read_text(), str(path))


if __name__ == '__main__': unittest.main()
