import importlib.util
from pathlib import Path
import unittest
from unittest import mock
from types import SimpleNamespace
import tempfile
import json

spec = importlib.util.spec_from_file_location('hdd', Path(__file__).with_name('hdd.py'))
hdd = importlib.util.module_from_spec(spec)
spec.loader.exec_module(hdd)


class HDDControls(unittest.TestCase):
    def test_exact_device_and_read_limits(self):
        self.assertEqual(hdd.io_limit('8:17', 50000000, 80),
                         '8:17 rbps=50000000 wbps=max riops=80 wiops=max')

    def test_rejects_wildcards_injection_and_unbounded_reads(self):
        for device, rbps, riops in [('max', 1, 1), ('8:0\n8:1', 1, 1),
                                    ('8:0 rbps=max', 1, 1), ('8:0', 0, 1),
                                    ('8:0', 1, -1)]:
            with self.assertRaises(ValueError):
                hdd.io_limit(device, rbps, riops)

    def test_run_refuses_without_optin_before_filesystem_or_process_work(self):
        with mock.patch.object(hdd.Path, 'resolve') as resolve, mock.patch.object(hdd.subprocess, 'Popen') as launch:
            with self.assertRaisesRegex(ValueError, 'opt-in'):
                hdd.run(SimpleNamespace(allow_scratch_io=False))
            resolve.assert_not_called()
            launch.assert_not_called()

    def test_run_refuses_system_nonscratch_and_undelegated_roots(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            args = SimpleNamespace(allow_scratch_io=True, root='/', parent='/sys/fs/cgroup/delegated', device=hdd.scratch_device(root), rbps=1, riops=1, command=['scanbench'])
            with mock.patch.object(hdd.Path, 'resolve', lambda p, **kw: p), mock.patch.object(hdd.Path, 'mkdir') as mkdir, mock.patch.object(hdd.subprocess, 'Popen') as launch:
                with self.assertRaises(ValueError):
                    hdd.run(args)
                args.root = temp
                with self.assertRaisesRegex(ValueError, 'completed scan fixture'):
                    hdd.run(args)
                (root / '.portico-scan-fixture.json').write_text(json.dumps({'version': 'scan-library-v1', 'files': 1}))
                args.parent = '/sys/fs/cgroup'
                with self.assertRaises(ValueError):
                    hdd.run(args)
                args.parent = '/sys/fs/cgroup/delegated'
                with mock.patch.object(hdd.os, 'access', return_value=False):
                    with self.assertRaisesRegex(ValueError, 'delegated writable'):
                        hdd.run(args)
                mkdir.assert_not_called()
                launch.assert_not_called()


if __name__ == '__main__':
    unittest.main()
