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
            line = next(line for line in text.splitlines() if boundary in line and 'python3 ' in line and name + '[@]' in line)
            expression = line.split(boundary)[1]
            script = ('set -eu\ncapture() { printf "%s\\n" "$#"; printf "<%s>\\n" "$@"; }\n'
                + name + '=()\ncapture ' + expression + '\n' + name + '=("with space" --flag)\ncapture ' + expression)
            result = subprocess.run(['/bin/bash', '-c', script], text=True, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.splitlines(), ['0', '<>', '2', '<with space>', '<--flag>'])
        print('Native system Bash empty-array proof: ' + platform.system() + ': ' + subprocess.check_output(['/bin/bash', '--version'], text=True).splitlines()[0], flush=True)

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

    def test_transport_keys_ignore_run_branch_and_attempt_but_bind_pins_and_compiler(self):
        transport = CI['module']('engine-dependency-cache')
        env = dict(PORTICO_ENGINE_BUILD_CONTEXT='development', GITHUB_ACTIONS='true',
                   RUNNER_ENVIRONMENT='github-hosted', GITHUB_REPOSITORY='PorticoMediaServer/portico',
                   GITHUB_EVENT_NAME='workflow_dispatch')
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); keys = []
            for index, (run, attempt, branch, image, pins) in enumerate([
                    ('1', '1', 'main', 'a', 'a'), ('2', '3', 'topic', 'a', 'a'),
                    ('3', '1', 'main', 'a', 'b'), ('4', '1', 'main', 'b', 'a')]):
                output = root / (str(index) + '.out')
                with patch.dict(os.environ, dict(env, GITHUB_RUN_ID=run, GITHUB_RUN_ATTEMPT=attempt,
                                               GITHUB_REF='refs/heads/' + branch)), \
                        patch.object(transport, 'compiler', return_value={'image': 'sha256:' + image * 64}), \
                        patch.object(transport, 'dependency_inputs', return_value={'pin': pins * 64}):
                    transport.plan('linux-x64', root / str(index), output)
                keys.append(dict(line.split('=', 1) for line in output.read_text().splitlines()))
            self.assertEqual(keys[0], keys[1])
            self.assertNotEqual(keys[0]['key'], keys[2]['key'])
            self.assertEqual(keys[0]['prefix'], keys[2]['prefix'])
            self.assertNotEqual(keys[0]['prefix'], keys[3]['prefix'])
            workflow = (ROOT / '.github/workflows/ffmpeg-toolchain.yml').read_text().split('  build:\n', 1)[1].split('  native-plan:\n', 1)[0]
            for runtime_key in ('github.run_id', 'github.run_attempt'):
                self.assertNotIn(runtime_key, workflow)
            # Branch identity may gate main-only cache writes; it is never a key.
            self.assertNotIn('uses: actions/cache/save', workflow)
            self.assertNotIn('uses: actions/cache/restore', workflow)
            self.assertIn('engine-cache-oci.py restore', workflow)

    def test_checkpoint_content_changes_only_with_completed_cache_data(self):
        transport = CI['module']('engine-dependency-cache')
        with tempfile.TemporaryDirectory() as temporary, patch.dict(os.environ, PORTICO_ENGINE_BUILD_CONTEXT='development'):
            root = Path(temporary)
            partial = root / 'macos/stages/.stage-incomplete'; partial.mkdir(parents=True)
            (partial / 'index.json').write_text('{}')
            self.assertEqual(transport.checkpoint(root), dict(present=False, snapshot='empty'))
            stage = root / 'macos/stages' / ('a' * 64); stage.mkdir()
            (stage / 'index.json').write_text('{"controlledFixture":true}')
            blob = stage / 'blob'; blob.write_bytes(b'cache payload')
            before = transport.checkpoint(root)
            self.assertTrue(before['present'])
            os.utime(blob, (1, 1)); (root / 'transport-context.json').write_text('{"run":999}')
            self.assertEqual(before, transport.checkpoint(root))
            blob.write_bytes(b'changed payload')
            self.assertNotEqual(before['snapshot'], transport.checkpoint(root)['snapshot'])
        with patch.dict(os.environ, PORTICO_ENGINE_BUILD_CONTEXT='release'), self.assertRaises(ValueError):
            transport.checkpoint(Path('/must-not-be-opened'))

    def test_only_four_supported_targets_and_source_cache_always_saves(self):
        self.assertEqual(set(CI['TARGETS']), {'linux-x64', 'linux-arm64', 'windows-x64', 'macos-arm64'})
        workflow = (ROOT / '.github/workflows/ffmpeg-toolchain.yml').read_text()
        self.assertIn('if: always() && (matrix.target', workflow)
        self.assertIn('engine-download-cache', workflow)
        self.assertIn('timeout=300 * 60', (ROOT / 'scripts/ci-ffmpeg-toolchain.py').read_text())
        for path in (ROOT / '.github/workflows').glob('*.yml'):
            self.assertNotIn('macos-15-intel', path.read_text(), str(path))

    @unittest.skipUnless(platform.system() == 'Linux' and os.environ.get('GITHUB_ACTIONS') == 'true',
                         'root/runner ownership boundary is exercised on hosted Linux')
    def test_linux_container_returns_root_private_checkpoints_on_success_and_failure(self):
        self.assertNotEqual(os.getuid(), 0, 'the checkpoint reader must be the unprivileged runner')
        transport = CI['module']('engine-dependency-cache')
        builder = CI['module']('build-linux-ffmpeg-deps')
        lock = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())
        with tempfile.TemporaryDirectory() as temporary, patch.dict(os.environ, PORTICO_ENGINE_BUILD_CONTEXT='development'):
            root = Path(temporary)
            try:
                for status in (0, 23):
                    case = root / str(status); case.mkdir()
                    work = case / 'work'; work.mkdir()
                    cache = case / 'cache'; cache.mkdir()
                    recipe = case / 'recipe'; recipe.mkdir()
                    with patch.dict(os.environ, PORTICO_ENGINE_STAGE_CACHE=str(cache)), patch.object(builder, 'run') as run:
                        builder.container(recipe, work, dict(lock['linuxSourceBuild'], sourceDateEpoch=lock['sourceDateEpoch']), [], 2)
                    self.assertIn(str(cache) + ':/portico-stage-cache', run.call_args.args)
                    stage = cache / 'linux-native/stages' / ('a' * 64)
                    # Same ownership/mode as root's tempfile.mkdtemp + rename
                    # in PrefixCache.finish; no compiler or Docker job is run.
                    subprocess.run(['sudo', '-n', 'python3', '-c',
                        'from pathlib import Path; import sys; p=Path(sys.argv[1]); '
                        'p.mkdir(parents=True, mode=0o700); '
                        '(p/"index.json").write_text("{}"); '
                        '(p/"payload").write_bytes(b"completed stage")', str(stage)], check=True)
                    with self.assertRaises(PermissionError): transport.checkpoint(cache)
                    # Execute the actual generated EXIT trap as root, just as
                    # in Docker, substituting only the disposable work mount.
                    script = (work / 'dependencies.sh').read_text().split('cd /recipe\n', 1)[0]
                    script = script.replace('/portico', str(work)) + '\nexit ' + str(status) + '\n'
                    result = subprocess.run(['sudo', '-n', 'env', 'PORTICO_ENGINE_STAGE_CACHE=' + str(cache),
                                             'bash', '-c', script])
                    self.assertEqual(result.returncode, status)
                    self.assertTrue(transport.checkpoint(cache)['present'])
                    self.assertEqual(stage.stat().st_uid, os.getuid())
                    self.assertEqual(stage.stat().st_mode & 0o777, 0o700)
                    self.assertEqual((stage / 'payload').read_bytes(), b'completed stage')
            finally:
                subprocess.run(['sudo', '-n', 'chown', '-hRP', str(os.getuid()) + ':' + str(os.getgid()), str(root)], check=True)
        print('Root-private Linux stage checkpoints readable by runner after success and exit23; bytes/modes preserved', flush=True)

    def test_constructed_product_upload_survives_cache_transport_failure(self):
        workflow = (ROOT / '.github/workflows/ffmpeg-toolchain.yml').read_text().split('  build:\n', 1)[1].split('  native-plan:\n', 1)[0]
        build = workflow.split('      - name: Build once from pinned source, retaining custody and cold timing\n', 1)[1]
        self.assertTrue(build.startswith('        id: engine-build\n'))
        upload = workflow.split('          name: engine-cold-${{ matrix.target }}-', 1)[0].rsplit('      - uses:', 1)[1]
        self.assertIn("if: ${{ !cancelled() && steps.engine-build.outcome == 'success' }}", upload)


if __name__ == '__main__': unittest.main()
