#!/usr/bin/env python3
"""Offline path checks for the FFmpeg build entrypoint.

Every external tool is mocked: no download, container, compiler, Go, FFmpeg,
network or database process runs. The checks cover scratch-root selection, the
published artifact directory, the checked source fence and output containment.
"""
import hashlib
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
DEFAULT_TARGET = 'linux-x64'


class EntrypointTests(unittest.TestCase):
    def setUp(self):
        scratch = ROOT / '.scratch' / 'reset-finish-packaging'
        scratch.mkdir(parents=True, exist_ok=True)
        self.temporary = tempfile.TemporaryDirectory(prefix='ffmpeg-entrypoint-', dir=scratch)
        self.addCleanup(self.temporary.cleanup)
        self.base = Path(self.temporary.name)
        self.root = self.base / 'checkout'
        (self.root / 'scripts').mkdir(parents=True)
        shutil.copy2(ROOT / 'scripts/build-ffmpeg.sh', self.root / 'scripts/build-ffmpeg.sh')
        lock_file = ROOT / 'third_party/ffmpeg/sources.lock.json'
        (self.root / 'third_party/ffmpeg').mkdir(parents=True)
        shutil.copy2(lock_file, self.root / 'third_party/ffmpeg/sources.lock.json')
        self.lock = json.loads(lock_file.read_text())
        self.log = self.base / 'invoked.log'
        self.log.write_text('')
        self.shims = self.base / 'bin'
        self.shims.mkdir()
        self.home = self.base / 'home'
        self.home.mkdir()
        self.python_shim()
        for name in ('curl', 'git', 'docker'):
            path = self.shims / name
            path.write_text(f'#!/usr/bin/env bash\nprintf "{name} %s\\n" "$*" >> "$HARNESS_LOG"\nexit 97\n')
            path.chmod(0o755)

    def python_shim(self):
        path = self.shims / 'python3'
        path.write_text(
            '#!/usr/bin/env bash\n'
            'printf "python3 %s\\n" "$*" >> "$HARNESS_LOG"\n'
            "env | grep -E '^(TMPDIR|GOTMPDIR|XDG_CACHE_HOME|PIP_CACHE_DIR|PORTICO_LANE_SCRATCH)='"
            ' >> "$HARNESS_LOG"\n'
            f'if [[ "$1" == "-c" ]]; then exec {shlex.quote(sys.executable)} "$@"; fi\n'
            'exit 0\n')
        path.chmod(0o755)

    def run_entrypoint(self, *arguments, target=DEFAULT_TARGET, env=None, source_only=True):
        environment = dict(os.environ)
        environment.update({
            'PATH': str(self.shims) + os.pathsep + environment['PATH'],
            'HOME': str(self.home),
            'HARNESS_LOG': str(self.log),
        })
        for key in ('PORTICO_LANE_SCRATCH', 'RUNNER_TEMP', 'PORTICO_FFMPEG_PHASE',
                    'PORTICO_DISC_SOURCE_COMMIT', 'PORTICO_DISC_FIXTURES',
                    'PORTICO_DISC_FIXTURES_SHA256'):
            environment.pop(key, None)
        if source_only:
            environment['PORTICO_FFMPEG_SOURCE_ONLY'] = '1'
        else:
            environment.pop('PORTICO_FFMPEG_SOURCE_ONLY', None)
        environment.update(env or {})
        return subprocess.run(
            ['bash', str(self.root / 'scripts/build-ffmpeg.sh'), target, *arguments],
            env=environment, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)

    def seed_archive(self, work):
        work.mkdir(parents=True, exist_ok=True)
        (work / 'jellyfin-ffmpeg-source.tar.gz').write_bytes(b'offline source placeholder')

    def expected_source(self, work):
        version = self.lock['ffmpeg']['version']
        patches = self.lock['ffmpeg'].get('postSeriesPatches', [])
        suffix = ''
        if patches:
            digest = hashlib.sha256(json.dumps(patches, sort_keys=True).encode()).hexdigest()[:12]
            suffix = '-' + digest
        return work / f'ffmpeg-{version}{suffix}.tar.xz'

    def invoked(self):
        return self.log.read_text().splitlines()

    def script_environment(self):
        values = {}
        for line in self.invoked():
            if line.startswith('python3 ') or '=' not in line:
                continue
            key, _, value = line.partition('=')
            if key in ('TMPDIR', 'GOTMPDIR', 'XDG_CACHE_HOME', 'PIP_CACHE_DIR',
                       'PORTICO_LANE_SCRATCH'):
                values[key] = value
        return values

    def assert_no_external_tools(self):
        self.assertEqual([line for line in self.invoked()
                          if line.split()[0] in ('curl', 'git', 'docker')], [])

    def assert_succeeded(self, result):
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(result.stdout.strip())

    def test_default_without_environment_uses_runtime_artifact_path(self):
        work = self.root / 'runtime/ffmpeg-build' / DEFAULT_TARGET
        self.seed_archive(work)
        result = self.run_entrypoint()
        self.assert_succeeded(result)
        self.assertEqual(result.stdout.strip().splitlines()[-1], str(self.expected_source(work)))
        self.assertEqual(work.relative_to(self.root), Path('runtime/ffmpeg-build/linux-x64'))
        environment = self.script_environment()
        self.assertEqual(environment['PORTICO_LANE_SCRATCH'], str(self.root / 'runtime'))
        self.assertEqual(environment['TMPDIR'], str(self.root / 'runtime/tmp'))
        self.assertEqual(environment['GOTMPDIR'], str(self.root / 'runtime/go-tmp'))
        self.assertEqual(environment['XDG_CACHE_HOME'], str(self.root / 'runtime/cache'))
        self.assertEqual(environment['PIP_CACHE_DIR'], str(self.root / 'runtime/cache/pip'))
        for path in ('runtime/tmp', 'runtime/go-tmp', 'runtime/cache/pip'):
            self.assertTrue((self.root / path).is_dir(), path)
        self.assert_no_external_tools()

    def test_runner_temp_keeps_runtime_artifact_and_uses_disk_caches(self):
        runner_temp = self.base / 'runner-temp'
        runner_temp.mkdir()
        work = self.root / 'runtime/ffmpeg-build' / DEFAULT_TARGET
        self.seed_archive(work)
        result = self.run_entrypoint(env={'RUNNER_TEMP': str(runner_temp)})
        self.assert_succeeded(result)
        self.assertEqual(result.stdout.strip().splitlines()[-1], str(self.expected_source(work)))
        self.assertFalse((runner_temp / 'ffmpeg-build').exists())
        environment = self.script_environment()
        self.assertEqual(environment['PORTICO_LANE_SCRATCH'], str(self.root / 'runtime'))
        self.assertEqual(environment['TMPDIR'], str(runner_temp / 'tmp'))
        self.assertEqual(environment['GOTMPDIR'], str(runner_temp / 'go-tmp'))
        self.assertEqual(environment['XDG_CACHE_HOME'], str(runner_temp / 'cache'))
        self.assertEqual(environment['PIP_CACHE_DIR'], str(runner_temp / 'cache/pip'))
        for path in ('tmp', 'go-tmp', 'cache/pip'):
            self.assertTrue((runner_temp / path).is_dir(), path)
        self.assert_no_external_tools()

    def test_explicit_lane_and_work_directory_are_honored(self):
        lane = self.base / 'jobs/hardware-r29/.scratch/hardware-r29'
        work = lane / 'ffmpeg-build/custom-target'
        self.seed_archive(work)
        result = self.run_entrypoint(str(work), env={'PORTICO_LANE_SCRATCH': str(lane)})
        self.assert_succeeded(result)
        self.assertEqual(result.stdout.strip().splitlines()[-1], str(self.expected_source(work)))
        environment = self.script_environment()
        self.assertEqual(environment['PORTICO_LANE_SCRATCH'], str(lane))
        self.assertEqual(environment['TMPDIR'], str(lane / 'tmp'))
        self.assertFalse((lane / 'ffmpeg-build' / DEFAULT_TARGET).exists())
        self.assert_no_external_tools()

    def test_relative_work_directory_inside_lane_is_honored(self):
        lane = self.base / 'jobs/hardware-r29/.scratch/hardware-r29'
        work = lane / 'relative-work'
        self.seed_archive(work)
        result = subprocess.run(
            ['bash', str(self.root / 'scripts/build-ffmpeg.sh'), DEFAULT_TARGET, 'relative-work'],
            cwd=lane, env={**os.environ, 'PATH': str(self.shims) + os.pathsep + os.environ['PATH'],
                           'HOME': str(self.home), 'HARNESS_LOG': str(self.log),
                           'PORTICO_LANE_SCRATCH': str(lane), 'PORTICO_FFMPEG_SOURCE_ONLY': '1'},
            text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.assert_succeeded(result)
        self.assertEqual(result.stdout.strip().splitlines()[-1], str(self.expected_source(work)))

    def test_output_outside_the_selected_scratch_is_refused(self):
        lane = self.base / 'jobs/hardware-r29/.scratch/hardware-r29'
        lane.mkdir(parents=True)
        outside = self.base / 'outside'
        outside.mkdir()
        (lane / 'escape').symlink_to(outside)
        for argument in (str(outside), str(lane / '..' / '..' / 'outside'), str(lane / 'escape')):
            result = self.run_entrypoint(argument, env={'PORTICO_LANE_SCRATCH': str(lane)})
            self.assertNotEqual(result.returncode, 0, argument)
            self.assertIn('Build output must stay under the scratch directory', result.stderr)
        result = self.run_entrypoint(str(self.base / 'default-escape'))
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Build output must stay under the scratch directory', result.stderr)
        self.assertEqual([path.name for path in outside.iterdir()], [])

    def test_lane_scratch_requires_the_dedicated_directory(self):
        ordinary = self.base / 'ordinary'
        ordinary.mkdir()
        result = self.run_entrypoint(env={'PORTICO_LANE_SCRATCH': str(ordinary)})
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Use the lane scratch directory on disk', result.stderr)

    def test_checked_disc_source_commit_fence_is_preserved(self):
        result = self.run_entrypoint(source_only=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Set PORTICO_DISC_SOURCE_COMMIT', result.stderr)
        result = self.run_entrypoint(source_only=False, env={'PORTICO_DISC_SOURCE_COMMIT': 'f' * 40})
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Disc helper commit differs from the canonical checked source lock',
                      result.stderr)
        self.assert_no_external_tools()

    def test_split_phase_guard_is_preserved(self):
        result = self.run_entrypoint(target='windows-x64', env={'PORTICO_FFMPEG_PHASE': 'compile'})
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Split compile/package phases currently require linux-x64', result.stderr)
        result = self.run_entrypoint(env={'PORTICO_FFMPEG_PHASE': 'unknown'})
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Unknown source build phase', result.stderr)


if __name__ == '__main__':
    unittest.main()
