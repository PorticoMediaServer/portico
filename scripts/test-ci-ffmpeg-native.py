#!/usr/bin/env python3
"""Pure negative controls; never start a build or container."""
import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import io
import tarfile
import os
import subprocess
import stat

spec = importlib.util.spec_from_file_location('native', Path(__file__).with_name('ci-ffmpeg-native.py'))
n = importlib.util.module_from_spec(spec)
spec.loader.exec_module(n)


def seed_libva(root):
    path = root / 'scripts.d/50-vaapi/50-libva.sh'
    path.parent.mkdir(parents=True)
    path.write_text('ffbuild_dockerbuild() {\n    meson "${myconf[@]}" ..\n}\n')


class TransferTests(unittest.TestCase):
    def test_paths(self):
        for value in ('../x', '/x', 'a/../b', 'C:/x', 'a\\b', 'a//b', './x'):
            with self.subTest(value=value), self.assertRaises(ValueError):
                n.safe_name(value)
        self.assertEqual(str(n.safe_name('input/bin/ffmpeg.exe')), 'input/bin/ffmpeg.exe')

    def test_nonfree(self):
        n.redistributable('--enable-gpl --enable-version3 --disable-nonfree --disable-libfdk-aac')
        for value in ('--enable-nonfree', '--enable-libfdk-aac'):
            with self.assertRaises(ValueError):
                n.redistributable('--enable-gpl --enable-version3 ' + value)

    def test_inventory_mutation_and_links(self):
        with tempfile.TemporaryDirectory() as work:
            root = Path(work)
            (root / 'input').mkdir()
            file = root / 'input/a'
            file.write_bytes(b'a')
            before = n.inventory(root)
            file.write_bytes(b'b')
            self.assertNotEqual(before, n.inventory(root))
            (root / 'input/link').symlink_to(file)
            with self.assertRaises(ValueError):
                n.inventory(root)

    def test_tar_traversal_and_incomplete(self):
        for name in ('../evil', 'recipe/build.sh'):
            with tempfile.TemporaryDirectory() as work:
                path = Path(work) / 'source.tar.xz'
                with tarfile.open(path, 'w:xz') as archive:
                    entry = tarfile.TarInfo(name)
                    entry.size = 1
                    archive.addfile(entry, io.BytesIO(b'x'))
                with self.assertRaises(ValueError):
                    n.corresponding_source(path)

    def test_source_cache_must_cover_dependency_stages(self):
        required = {
            'recipe/build.sh': b'', 'recipe/makeimage.sh': b'',
            'provenance/contract/sources.lock.json': b'{}',
            'provenance/contract/build-source-authority.json': b'{}',
            'provenance/scripts/build-ffmpeg.sh': b'',
            'provenance/scripts/ci-ffmpeg-native.py': b'adapter',
            'provenance/portico-disc/LICENSE': b'GPL',
            'provenance/portico-disc/portico-disc.c': b'code',
            'provenance/contract/patches/a.patch': b'patch',
            'recipe/scripts.d/50-codec.sh': b'SCRIPT_REPO="https://example.invalid/source"',
        }
        with tempfile.TemporaryDirectory() as work:
            archive = Path(work) / 'source.tar.xz'
            for present in [False, True]:
                files = dict(required)
                if present:
                    files['recipe/.cache/downloads/50-codec_hash.tar.xz'] = b'actual source snapshot'
                with tarfile.open(archive, 'w:xz') as tar:
                    for name, data in files.items():
                        item = tarfile.TarInfo(name); item.size = len(data)
                        tar.addfile(item, io.BytesIO(data))
                if present: n.corresponding_source(archive)
                else:
                    with self.assertRaises(ValueError): n.corresponding_source(archive)

    def test_host_arch_and_custody(self):
        with patch.object(n.platform, 'system', return_value='Linux'):
            with self.assertRaises(ValueError):
                n.native('windows-arm64')
        with patch.object(n, 'head', return_value='a'*40), patch.dict(n.os.environ, {'GITHUB_ACTIONS':'true', 'GITHUB_SHA':'b'*40, 'GITHUB_RUN_ID':'1'}, clear=True):
            with self.assertRaises(ValueError):
                n.context()

    def test_pr_checkout_uses_head_not_merge_ref(self):
        import json
        with tempfile.TemporaryDirectory() as work:
            event = Path(work) / 'event.json'
            event.write_text(json.dumps({'pull_request': {'head': {'sha': 'a'*40, 'repo': {'full_name': 'PorticoMediaServer/portico'}}}}))
            env = {'GITHUB_ACTIONS':'true','GITHUB_SHA':'b'*40,'GITHUB_RUN_ID':'1',
                   'GITHUB_EVENT_NAME':'pull_request','GITHUB_EVENT_PATH':str(event)}
            with patch.object(n, 'head', return_value='a'*40), patch.dict(n.os.environ, env, clear=True):
                self.assertEqual(n.context()['sourceSHA'], 'a'*40)
            with patch.object(n, 'head', return_value='c'*40), patch.dict(n.os.environ, env, clear=True):
                with self.assertRaises(ValueError): n.context()

    def test_compiled_configuration_not_shell_source(self):
        with tempfile.TemporaryDirectory() as work:
            binary = Path(work) / 'ffmpeg.exe'
            binary.write_bytes(b'MZ\0configuration: --enable-gpl --enable-version3 --disable-nonfree --disable-libfdk-aac\0')
            self.assertEqual(len(n.binary_configuration(binary)), 1)
            binary.write_bytes(b'MZ\0--enable-gpl --enable-version3 --enable-nonfree\0')
            with self.assertRaises(ValueError): n.binary_configuration(binary)
            binary.write_bytes(b'MZ opaque binary without configuration')
            with self.assertRaises(ValueError): n.binary_configuration(binary)

    def test_resource_adaptation(self):
        with tempfile.TemporaryDirectory() as work:
            root = Path(work)
            seed_libva(root)
            (root / 'makeimage.sh').write_text('max-parallelism = 4\nffbuilder --driver-opt network=host\n')
            (root / 'build.sh').write_text('docker run --rm\nmake -j$(nproc)\nninja install\n')
            (root / 'Dockerfile').write_text('FROM base\nRUN make -j$(nproc)\n')
            (root / 'download.sh').write_text('docker run -i --rm image\n')
            with patch.object(n.platform, 'system', return_value='Linux'), patch.dict(n.os.environ, {'GITHUB_ACTIONS':'true','RUNNER_ENVIRONMENT':'github-hosted','GITHUB_RUN_ID':'123'}):
                n.prepare_recipe(root)
            self.assertIn('CARGO_BUILD_JOBS=2', (root / 'Dockerfile').read_text())
            self.assertIn('ninja -j2 install', (root / 'build.sh').read_text())
            self.assertIn('--memory 4g', (root / 'build.sh').read_text())
            self.assertIn('--cpus 2 --memory 4g', (root / 'download.sh').read_text())
            self.assertNotIn('ffbuilder', (root / 'makeimage.sh').read_text())

    def test_bootstrap_make_is_serial_but_compile_can_use_two_jobs(self):
        # Bootstrap makefiles can omit ordering edges between cleanup and
        # generated macros. Do not add parallelism to their plain `make` calls.
        with tempfile.TemporaryDirectory() as work:
            root = Path(work)
            seed_libva(root)
            (root / 'makeimage.sh').write_text('max-parallelism = 4\nffbuilder --driver-opt network=host\n')
            (root / 'build.sh').write_text('docker run --rm\n')
            (root / 'Dockerfile').write_text('FROM base\n')
            with patch.object(n.platform, 'system', return_value='Linux'), patch.dict(n.os.environ, {'GITHUB_ACTIONS':'true','RUNNER_ENVIRONMENT':'github-hosted','GITHUB_RUN_ID':'123'}):
                n.prepare_recipe(root)
            line = next(x for x in (root / 'Dockerfile').read_text().splitlines() if x.startswith('ENV '))
            env = dict(os.environ)
            env.update(token.split('=', 1) for token in line[4:].split())
            # The generated container path is not a host test directory.
            env['TMPDIR'] = str(root)
            env.pop('MFLAGS', None)
            env.pop('MAKELEVEL', None)
            (root / 'Makefile').write_text('all:\n\t@echo $(MAKEFLAGS)\n')
            def flags(*args):
                return subprocess.check_output(['make', '--no-print-directory', *args], cwd=root, env=env, text=True).split()
            self.assertIn('-j1', flags())
            self.assertIn('-j2', flags('-j2'))

    def test_each_container_stage_has_created_scratch_before_build(self):
        with tempfile.TemporaryDirectory() as work:
            root = Path(work)
            seed_libva(root)
            (root / 'makeimage.sh').write_text('max-parallelism = 4\nffbuilder --driver-opt network=host\n')
            (root / 'build.sh').write_text('docker run --rm\n')
            (root / 'Dockerfile').write_text('FROM base AS first\nRUN compile-first\nFROM base AS second\nRUN compile-second\n')
            with patch.object(n.platform, 'system', return_value='Linux'), patch.dict(n.os.environ, {'GITHUB_ACTIONS':'true','RUNNER_ENVIRONMENT':'github-hosted','GITHUB_RUN_ID':'123'}):
                n.prepare_recipe(root)
            stages = (root / 'Dockerfile').read_text().split('FROM ')[1:]
            self.assertEqual(len(stages), 2)
            for stage in stages:
                lines = stage.splitlines()
                self.assertIn('TMPDIR=/var/tmp/portico-ci', lines[1].split())
                # Exercise the exact creation command on test-owned scratch.
                # The pinned downloader runs as the hosted runner UID, while
                # Docker image setup runs as root. Both need a normal sticky
                # temp directory, without changing any host-global path.
                scratch = root / ('scratch-' + str(stages.index(stage)))
                subprocess.run(['sh', '-ec', lines[2][4:]], env=dict(os.environ, TMPDIR=str(scratch)), check=True)
                self.assertEqual(stat.S_IMODE(scratch.stat().st_mode), 0o1777)
                self.assertTrue(lines[3].startswith('RUN compile-'))

    def test_native_apple_libtool_resolved_without_changing_job_limit(self):
        with tempfile.TemporaryDirectory() as work:
            tool = Path(work) / 'libtool'
            tool.write_text('#!/bin/sh\nexit 0\n')
            tool.chmod(0o755)
            with patch.object(n.platform, 'system', return_value='Darwin'), patch.object(n.subprocess, 'check_output', return_value=str(tool)+'\n') as resolve, patch.dict(n.os.environ, {'MACOS_LIBTOOL':'/wrong/gnu/libtool'}):
                env = n.build_env()
                self.assertEqual(env['MACOS_LIBTOOL'], str(tool))
                self.assertEqual(env['NUMJOBS'], '2')
                resolve.assert_called_once_with(['/usr/bin/xcrun', '--find', 'libtool'], text=True)
            for invalid in ('libtool', str(Path(work) / 'missing'), str(tool)):
                tool.chmod(0o644)
                with self.subTest(invalid=invalid), patch.object(n.platform, 'system', return_value='Darwin'), patch.object(n.subprocess, 'check_output', return_value=invalid):
                    with self.assertRaises(ValueError): n.build_env()

    def test_libva_failure_log_is_bounded_and_preserves_failure(self):
        source = 'ffbuild_dockerbuild() {\n    meson "${myconf[@]}" ..\n    echo COMPILE_REACHED\n}\nffbuild_dockerbuild\n'
        diagnostic = n.libva_diagnostics(source)
        with self.assertRaises(ValueError): n.libva_diagnostics('changed recipe')
        with tempfile.TemporaryDirectory() as work:
            root = Path(work)
            (root / 'meson-logs').mkdir()
            log = root / 'meson-logs/meson-log.txt'
            log.write_text('x' * 1048576 + 'OUTSIDE_BOUND')
            for status in (0, 37):
                command = 'meson() { return ' + str(status) + '; }\n' + diagnostic
                result = subprocess.run(['bash', '-e', '-c', command], cwd=root, capture_output=True, text=True)
                self.assertEqual(result.returncode, status)
                self.assertEqual('COMPILE_REACHED' in result.stdout, status == 0)
                self.assertEqual('BEGIN libva' in result.stdout, status != 0)
                self.assertNotIn('OUTSIDE_BOUND', result.stdout)
                self.assertLess(len(result.stdout), 1048576 + 200)
            log.unlink()
            result = subprocess.run(['bash', '-e', '-c', 'meson() { return 37; }\n' + diagnostic], cwd=root, capture_output=True, text=True)
            self.assertEqual(result.returncode, 37)
            self.assertIn('Meson log unavailable', result.stdout)


if __name__ == '__main__':
    unittest.main()
