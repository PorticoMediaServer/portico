import importlib.util
import os
from pathlib import Path
import tempfile
import threading
import unittest
import urllib.request


# The command installs an HTTP handler for its own users. Do not leak that
# command-line setup into the rest of the Python test runner.
_opener = urllib.request.build_opener
try:
    _spec = importlib.util.spec_from_file_location('server_load', Path(__file__).resolve().parents[1] / 'scripts/server-load.py')
    load = importlib.util.module_from_spec(_spec)
    _spec.loader.exec_module(load)
finally:
    urllib.request.build_opener = _opener


class ResidentMemory(unittest.TestCase):
    def test_linux_status_units_and_unavailable_samples(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            status = root / '123/status'
            status.parent.mkdir()
            for raw, expected in [('Name:\tserver\nVmRSS:\t1234 kB\n', 1234 * 1024),
                                  ('VmRSS: 0 kB\n', 0), ('Name: server\n', None),
                                  ('VmRSS: -1 kB\n', None), ('VmRSS: x kB\n', None),
                                  ('VmRSS: 12 MB\n', None)]:
                status.write_text(raw)
                self.assertEqual(load.read_rss_bytes(123, root), expected)
            status.unlink()
            self.assertIsNone(load.read_rss_bytes(123, root))
        if Path('/proc/self/status').exists():
            self.assertGreater(load.read_rss_bytes(os.getpid()), 0)

    def test_sampler_runs_during_blocking_work_and_stops_on_failure(self):
        sampled = threading.Event()
        reads = []

        def read(_pid):
            reads.append(1024 * len(reads))
            if len(reads) > 1:
                sampled.set()
            return reads[-1]

        with self.assertRaisesRegex(RuntimeError, 'profile failed'):
            with load.resident_memory(123, read=read, interval=.001) as samples:
                self.assertTrue(sampled.wait(1), 'RSS did not sample while the phase thread was blocked')
                raise RuntimeError('profile failed')
        self.assertGreaterEqual(len(samples), 3)  # start, independent sample, end
        self.assertFalse(any(t.name == 'server-load-rss' for t in threading.enumerate()))
        with load.resident_memory(123, read=lambda _pid: None) as samples:
            pass
        self.assertEqual(samples, [])

    def test_summary_labels_only_available_server_memory(self):
        out = {'phases': [{'users': 10, 'label': 'idle', 'perSecond': 1, 'ops': {},
                           'rssBytes': {'samples': 2, 'first': 1024, 'last': 2097152, 'peak': 2097152}},
                          {'users': 10, 'label': 'busy', 'perSecond': 1, 'ops': {}}]}
        report = load.summary(out, [])
        self.assertIn('Server RSS peak', report)
        self.assertIn('2.0 MiB', report)
        self.assertIn('| — | 0 |', report)


if __name__ == '__main__':
    unittest.main()
