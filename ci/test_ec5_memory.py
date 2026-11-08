import os
import tempfile
from pathlib import Path
import unittest
from unittest.mock import patch
import ec5_memory as memory

SHA = 'a1b2c3d4' * 8

class MemoryProtocol(unittest.TestCase):
    def test_missing_is_not_zero_and_peak_is_charged(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            absent = memory.snapshot(root)
            self.assertIsNone(memory.compare(absent)['kernel_charged_peak_bytes'])
            self.assertIsNone(memory.compare(absent)['kernel_peak_headroom_bytes'])
            (root / 'memory.max').write_text('1999998976')
            (root / 'memory.peak').write_text('0')
            observed = memory.compare(memory.snapshot(root))
            self.assertEqual(observed['kernel_charged_peak_bytes'], 0)
            self.assertEqual(observed['kernel_peak_headroom_bytes'], 1999998976)
            self.assertIsNone(observed['whole_server_fit'])
            (root / 'memory.peak').write_text('2000000000')
            self.assertEqual(memory.compare(memory.snapshot(root))['kernel_peak_headroom_bytes'], -1024)
            self.assertFalse(memory.compare(memory.snapshot(root))['qualification'])


    def test_successful_empty_or_skipped_child_cannot_claim_execution(self):
        name = 'TestEC5MemoryChildControl'
        self.assertFalse(memory.executed_parent('testing: warning: no tests to run\nPASS\n', name))
        self.assertFalse(memory.executed_parent('=== RUN   ' + name + '\n--- SKIP: ' + name + ' (0s)\nPASS\n', name))
        self.assertTrue(memory.executed_parent('=== RUN   ' + name + '\n--- PASS: ' + name + ' (0.1s)\nPASS\n', name))


class PopulationBinding(unittest.TestCase):
    def test_both_omitted_preserves_every_legacy_target(self):
        for target in ('control', 'paired', 'sustained'):
            with self.subTest(target=target):
                self.assertEqual(memory.population_environment(None, None, target), {})

    def test_exactly_one_relative_directory_and_bad_pin_fail(self):
        for directory, sha in (
                (Path('/private/population'), None),
                (None, SHA),
                (Path('population'), SHA),
                (Path('/private/population'), SHA.upper()),
                (Path('/private/population'), SHA[:63]),
                (Path('/private/population'), SHA + '0'),
                (Path('/private/population'), 'g' * 64),
                (Path('/private/population'), '0x' + SHA[:62])):
            with self.subTest(directory=directory, sha=sha), self.assertRaises(ValueError):
                memory.population_environment(directory, sha, 'paired')

    def test_control_rejects_any_supplied_population(self):
        for directory, sha in ((Path('/private/population'), SHA), (Path('/private/population'), None), (None, SHA)):
            with self.subTest(directory=directory, sha=sha), self.assertRaises(ValueError):
                memory.population_environment(directory, sha, 'control')

    def test_paired_and_sustained_forward_only_the_two_child_variables(self):
        expected = {'PORTICO_PAIRED_POPULATION_DIRECTORY': '/private/population',
                    'PORTICO_PAIRED_POPULATION_MANIFEST_SHA256': SHA}
        for target in ('paired', 'sustained'):
            with self.subTest(target=target):
                env = {'PATH': '/usr/bin', 'PORTICO_EC5_MEMORY': '1'}
                env.update(memory.population_environment(Path('/private/population'), SHA, target))
                self.assertEqual(env, {'PATH': '/usr/bin', 'PORTICO_EC5_MEMORY': '1', **expected})
                self.assertEqual(set(memory.population_environment(Path('/private/population'), SHA, target)), set(expected))

    def test_invalid_pair_fails_before_output_or_cgroup_mutation(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            binary = root / 'binary'
            binary.write_text('')
            binary.chmod(0o755)
            cgroup = root / 'cgroup'
            cgroup.mkdir()
            output = root / 'output'
            with patch.dict(os.environ, {'SUDO_UID': '1234', 'SUDO_GID': '1234'}, clear=True), \
                    patch.object(memory.os, 'geteuid', return_value=0):
                with self.assertRaises(ValueError):
                    memory.run(binary, output, 'paired', root / 'config', False, Path('relative'), SHA)
                with self.assertRaises(ValueError):
                    memory.run(binary, output, 'control', None, False, Path('/private/population'), SHA)
            self.assertFalse(output.exists())
            self.assertEqual(list(cgroup.iterdir()), [])
