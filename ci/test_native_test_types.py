import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from run import js_static


class NativeTestTypes(unittest.TestCase):
    def run_static(self, fail_tests=False):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            native = root / 'apps/native'
            native.mkdir(parents=True)
            (native / 'tsconfig.json').write_text('{}')
            (native / 'package.json').write_text(json.dumps({'scripts': {}}))
            plan = {'js': [{'component': 'apps/native', 'static_roots': ['apps/native']}]}
            def execute(args, cwd):
                if fail_tests and 'test/tsconfig.json' in args:
                    raise subprocess.CalledProcessError(2, args)
            with patch('run.ROOT', root), patch('run.validate_selection'), patch('run.execute', side_effect=execute) as command:
                js_static(plan, 'apps/native')
                return command.call_args_list, native

    def test_native_static_gate_compiles_all_tests_as_well_as_app(self):
        calls, native = self.run_static()
        self.assertTrue(any(call.args == (['npx', '--no-install', 'tsc', '--noEmit', '--project', 'test/tsconfig.json'], native) for call in calls))
        self.assertTrue(any(call.args == (['npx', '--no-install', 'tsc', '--noEmit'], native) for call in calls))

    def test_native_test_type_errors_fail_the_gate(self):
        with self.assertRaises(subprocess.CalledProcessError):
            self.run_static(fail_tests=True)
