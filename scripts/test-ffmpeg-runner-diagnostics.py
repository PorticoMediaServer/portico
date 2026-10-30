#!/usr/bin/env python3
"""Bounded text-collection checks; these do not run or qualify FFmpeg."""
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('ffmpeg_runner_build',
    Path(__file__).with_name('run-ffmpeg-runner-build.py'))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)
owner_spec = importlib.util.spec_from_file_location('ffmpeg_runner_owner',
    Path(__file__).with_name('repair-ffmpeg-runner-ownership.py'))
owner = importlib.util.module_from_spec(owner_spec)
owner_spec.loader.exec_module(owner)


class DiagnosticsTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(dir=os.environ['TMPDIR'])
        self.addCleanup(self.directory.cleanup)
        self.job = Path(self.directory.name)
        self.output = self.job / 'outputs'
        self.output.mkdir()
        self.work = self.job / '.scratch/hardware-r29/ffmpeg-build/linux-x64'
        self.work.mkdir(parents=True)
        self.environment = patch.dict(os.environ, PORTICO_RUNNER_OUTPUT=str(self.output))
        self.environment.start()
        self.addCleanup(self.environment.stop)

    def collected(self, succeeded=False, failure='controlled failure'):
        runner.collect_qualification_reports(self.job, 'a' * 40, succeeded, failure)
        path = self.output / 'hardware-qualification.log'
        self.assertEqual(path.stat().st_mode & 0o777, 0o644)
        return json.loads(path.read_text())

    def test_missing_reports_do_not_claim_a_gate(self):
        record = self.collected()
        self.assertFalse(record['canonicalBuildCommandSucceeded'])
        self.assertEqual(record['failure'], 'controlled failure')
        self.assertEqual(set(record['reports']), set(runner.QUALIFICATION_REPORTS))
        self.assertTrue(all(r['state'] == 'not-produced' for r in record['reports'].values()))

    def test_success_copies_json_arrays_and_binary_bindings(self):
        value = [{'round': 1, 'binaries': {'ffmpeg': 'b' * 64}}]
        (self.work / 'repeat-build.json').write_text(json.dumps(value))
        record = self.collected(True, None)
        self.assertTrue(record['canonicalBuildCommandSucceeded'])
        self.assertEqual(record['reports']['repeat-build.json']['report'], value)
        self.assertEqual(record['sourceCommit'], 'a' * 40)

    def test_truncated_report_is_kept_as_failed_text(self):
        (self.work / 'repeat-build.json').write_text('{"unfinished":')
        record = self.collected()['reports']['repeat-build.json']
        self.assertEqual(record['state'], 'invalid-json')
        self.assertEqual(record['text'], '{"unfinished":')
        self.assertNotIn('report', record)

    def test_file_link_is_refused(self):
        target = self.job / 'synthetic-unrelated.json'
        target.write_text('{"mustNotCopy":true}')
        (self.work / 'repeat-build.json').symlink_to(target)
        self.assertEqual(self.collected()['reports']['repeat-build.json']['state'], 'refused')
        self.assertNotIn('mustNotCopy', (self.output / 'hardware-qualification.log').read_text())

    def test_directory_link_is_refused(self):
        target = self.job / 'synthetic-other-directory'
        target.mkdir()
        (target / 'dovi-video-build.json').write_text('{"mustNotCopy":true}')
        (self.work / 'linux-output').symlink_to(target, target_is_directory=True)
        self.assertEqual(self.collected()['reports']['linux-output/dovi-video-build.json']['state'], 'refused')
        self.assertNotIn('mustNotCopy', (self.output / 'hardware-qualification.log').read_text())

    def test_oversize_report_is_refused(self):
        (self.work / 'repeat-build.json').write_bytes(b' ' * ((2 << 20) + 1))
        self.assertEqual(self.collected()['reports']['repeat-build.json']['state'], 'refused')

    def test_linked_scratch_ancestor_is_refused(self):
        outside = self.job / 'synthetic-other-scratch'
        (self.job / '.scratch').rename(outside)
        (self.job / '.scratch').symlink_to(outside, target_is_directory=True)
        record = self.collected()
        self.assertTrue(all(r['state'] == 'refused' for r in record['reports'].values()))

    def test_non_text_report_is_refused(self):
        (self.work / 'repeat-build.json').write_bytes(b'\xff\xfe')
        self.assertEqual(self.collected()['reports']['repeat-build.json']['state'], 'refused')

    def test_fifo_is_refused_without_waiting_for_a_writer(self):
        os.mkfifo(self.work / 'repeat-build.json')
        self.assertEqual(self.collected()['reports']['repeat-build.json']['state'], 'refused')

    def test_foreign_output_is_refused(self):
        outside = self.job / 'another-output'
        outside.mkdir()
        with patch.dict(os.environ, PORTICO_RUNNER_OUTPUT=str(outside)):
            with self.assertRaises(ValueError):
                self.collected()
        self.assertEqual(list(outside.iterdir()), [])

    def test_linked_output_is_refused(self):
        outside = self.job / 'another-output'
        outside.mkdir()
        self.output.rmdir()
        self.output.symlink_to(outside, target_is_directory=True)
        with self.assertRaises(ValueError):
            self.collected()
        self.assertEqual(list(outside.iterdir()), [])

    def test_initial_record_is_atomically_replaced_by_final_record(self):
        record = self.collected(None, 'not started')
        self.assertIsNone(record['canonicalBuildCommandSucceeded'])
        (self.work / 'repeat-build.json').write_text('[]')
        record = self.collected(True, None)
        self.assertTrue(record['canonicalBuildCommandSucceeded'])
        self.assertEqual(record['reports']['repeat-build.json']['state'], 'produced')
        self.assertEqual(sorted(p.name for p in self.output.iterdir()), ['hardware-qualification.log'])

    def test_stop_cleanup_cannot_download_an_image(self):
        owned_job = self.job / 'jobs/hardware-r29/aaaaaaaaaaaa-12345678'
        owned_job.mkdir(parents=True)
        with patch.object(owner.subprocess, 'run') as command:
            owner.restore_job_ownership(owned_job, 'exact-retained-compiler-image')
        command.assert_called_once()
        arguments = command.call_args.args[0]
        self.assertEqual(arguments[arguments.index('--pull') + 1], 'never')
        self.assertEqual(arguments[arguments.index('--network') + 1], 'none')


if __name__ == '__main__':
    result = unittest.TextTestRunner(verbosity=2).run(unittest.defaultTestLoader.loadTestsFromTestCase(DiagnosticsTests))
    output = Path(os.environ['PORTICO_RUNNER_OUTPUT']) / 'runner-diagnostics-tests.log'
    output.write_text(json.dumps({'tests': result.testsRun, 'failures': len(result.failures),
        'errors': len(result.errors), 'skips': len(result.skipped),
        'scope': 'Controlled diagnostic collector cases only; no FFmpeg build or target acceptance.'}, indent=2) + '\n')
    raise SystemExit(0 if result.wasSuccessful() else 1)
