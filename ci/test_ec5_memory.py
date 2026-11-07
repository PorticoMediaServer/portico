import tempfile
from pathlib import Path
import unittest
import ec5_memory as memory

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
