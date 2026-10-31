#!/usr/bin/env python3
from pathlib import Path
import runpy
import unittest
from unittest.mock import patch

M = runpy.run_path(str(Path(__file__).with_name('verify-native-engine.py')))


class NativeContext(unittest.TestCase):
    def test_wrong_os_and_cpu_are_not_native_qualification(self):
        with patch('platform.system', return_value='Linux'), patch('platform.machine', return_value='x86_64'):
            with self.assertRaises(ValueError):
                M['native']('windows-x64', 'local', {})
        with patch('platform.system', return_value='Windows'), patch('platform.machine', return_value='amd64'):
            M['native']('windows-x64', 'local', {})
            with self.assertRaises(ValueError):
                M['native']('windows-arm64', 'local', {'PROCESSOR_ARCHITECTURE': 'AMD64'})

    def test_arm_python_and_local_context_are_explicit(self):
        with patch('platform.system', return_value='Windows'), patch('platform.machine', return_value='ARM64'):
            M['native']('windows-arm64', 'local', {'PROCESSOR_ARCHITECTURE': 'ARM64'})
            for env in ({'PROCESSOR_ARCHITECTURE': 'AMD64'},
                        {'PROCESSOR_ARCHITECTURE': 'ARM64', 'GITHUB_ACTIONS': 'true'},
                        {'PROCESSOR_ARCHITECTURE': 'ARM64', 'PORTICO_RELEASE_SIGNING_KEY_FILE': ''}):
                with self.assertRaises(ValueError):
                    M['native']('windows-arm64', 'local', env)


if __name__ == '__main__':
    unittest.main()
