import importlib.util
from pathlib import Path
import unittest

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


if __name__ == '__main__':
    unittest.main()
