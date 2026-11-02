#!/usr/bin/env python3
"""Regression controls for exact source reuse, replay and four-target planning."""
import hashlib
import io
import lzma
import json
import os
from pathlib import Path
import runpy
import tempfile
import tarfile
import subprocess
import shutil
import shlex
import platform
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
                    if mode == 'valid':
                        alias = tarfile.TarInfo('recipe/.cache/downloads/50-library.tar.xz')
                        alias.type = tarfile.SYMTYPE; alias.linkname = Path(name).name
                        out.addfile(alias)
                    ignored = tarfile.TarInfo('recipe/bin/never-execute'); ignored.size = 3
                    out.addfile(ignored, io.BytesIO(b'bin'))
                if mode == 'valid':
                    self.assertEqual(seed['snapshots'](source, root / mode), 1)
                    self.assertFalse((root / mode / 'bin').exists())
                else:
                    with self.assertRaises(ValueError): seed['snapshots'](source, root / mode)

    def test_new_snapshot_packing_is_reproducible_without_rewriting_seeded_archives(self):
        gnu = shutil.which('gtar') or shutil.which('tar')
        if not gnu or b'GNU tar' not in subprocess.check_output([gnu, '--version']):
            self.skipTest('GNU tar exercised by hosted Linux; native Mac separately proves Bash 3.2')
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); recipe = root / 'recipe'; recipe.mkdir()
            (recipe / 'download.sh').write_text('set -eu\ncd "$1"\nTGT="$2"\n'
                + 'tar() { ' + shlex.quote(gnu) + ' "$@"; }\n'
                + 'tar -I "xz -T1" -cpf "$TGT.tmp" .\nmv "$TGT.tmp" "$TGT"\n')
            cached = recipe / '.cache/downloads'; cached.mkdir(parents=True)
            legacy = cached / ('50-existing_' + 'a' * 64 + '.tar.xz')
            legacy.write_bytes(lzma.compress(b'old exact source snapshot'))
            before = CACHE['digest'](legacy)
            CACHE['configure_download'](recipe); script = (recipe / 'download.sh').read_bytes()
            CACHE['configure_download'](recipe)
            self.assertEqual(script, (recipe / 'download.sh').read_bytes())
            self.assertEqual(before, CACHE['digest'](legacy))
            hashes = []
            for index, order in enumerate((('z', 'a', 'tool'), ('tool', 'a', 'z'))):
                tree = root / ('tree' + str(index)); tree.mkdir()
                for name in order:
                    path = tree / name; path.write_bytes(name.encode()); path.chmod(0o755 if name == 'tool' else 0o644)
                    os.utime(path, (100 + index, 100 + index))
                (tree / 'link').symlink_to('a')
                output = root / (str(index) + '.tar.xz')
                subprocess.run(['/bin/bash', recipe / 'download.sh', tree, output],
                    env=dict(os.environ, SOURCE_DATE_EPOCH='1234567890'), check=True)
                hashes.append(CACHE['digest'](output))
                with tarfile.open(output) as archive:
                    self.assertEqual(archive.extractfile('./a').read(), b'a')
                    self.assertEqual(archive.getmember('./link').linkname, 'a')
                    self.assertEqual(archive.getmember('./tool').mode & 0o111, 0o111)
                    self.assertTrue(all(m.uid == m.gid == 0 and m.mtime == 1234567890 for m in archive.getmembers()))
            self.assertEqual(hashes[0], hashes[1])

    def test_actual_empty_array_expansions_work_on_native_system_bash(self):
        text = (ROOT / 'scripts/build-ffmpeg.sh').read_text()
        for name, boundary in (('compile_options', ' --source "$SOURCE" '),
                               ('normalize_options', ' "$WORK/corresponding-source.tar.xz" ')):
            line = next(line for line in text.splitlines() if boundary in line and 'python3 ' in line)
            expression = line.split(boundary)[1]
            script = ('set -eu\ncapture() { printf "%s\\n" "$#"; printf "<%s>\\n" "$@"; }\n'
                + name + '=()\ncapture ' + expression + '\n' + name + '=("with space" --flag)\ncapture ' + expression)
            result = subprocess.run(['/bin/bash', '-c', script], text=True, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.splitlines(), ['0', '<>', '2', '<with space>', '<--flag>'])
        print('Native system Bash empty-array proof: ' + platform.system(), flush=True)

    def test_native_rerun_keeps_original_attempt_and_rejects_future_cross_run_cross_source(self):
        self.assertEqual(CI['native_retry_pin']('windows-x64', replay_run_id='41346289584')['origin']['runId'], 41346289584)
        with self.assertRaises(ValueError): CI['native_retry_pin']('windows-x64', replay_run_id='1')
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

    def test_cache_report_counts_real_receipts_without_claiming_unreadable_ones_are_misses(self):
        with tempfile.TemporaryDirectory() as temporary:
            work = Path(temporary); scratch = work / '.scratch'
            receipts = scratch / 'ffmpeg-build/macos-arm64/dependency-cache-receipts'; receipts.mkdir(parents=True)
            (receipts / 'hit.json').write_text('{"cacheHit":true}')
            (receipts / 'miss.json').write_text('{"cacheHit":false}')
            (receipts / 'bad.json').write_text('invalid')
            (work / 'evidence').mkdir(); (work / 'evidence/build.log').write_bytes(b'#1 CACHED\n#2 RUN make\n#3 CACHED\n')
            result = CI['cache_usage'](work, scratch, 'macos-arm64')
            self.assertEqual((result['dependencyReceiptHits'], result['dependencyReceiptMisses']), (1, 1))
            self.assertEqual(result['cachedBuildKitOperationLogLines'], 2)
            self.assertEqual(len(result['receiptReadFailures']), 1)

    def test_only_four_supported_targets_and_source_cache_always_saves(self):
        self.assertEqual(set(CI['TARGETS']), {'linux-x64', 'linux-arm64', 'windows-x64', 'macos-arm64'})
        workflow = (ROOT / '.github/workflows/ffmpeg-toolchain.yml').read_text()
        self.assertIn('if: always() && (matrix.target', workflow)
        self.assertIn('engine-download-cache', workflow)
        self.assertIn('timeout=300 * 60', (ROOT / 'scripts/ci-ffmpeg-toolchain.py').read_text())
        for path in (ROOT / '.github/workflows').glob('*.yml'):
            self.assertNotIn('macos-15-intel', path.read_text(), str(path))


if __name__ == '__main__': unittest.main()
