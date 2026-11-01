#!/usr/bin/env python3
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('build_tools', Path(__file__).with_name('ffmpeg-build-tools.py'))
M = importlib.util.module_from_spec(spec); spec.loader.exec_module(M)
REQUIREMENTS = Path(__file__).with_name('requirements-ffmpeg-meson.txt')


class MesonPin(unittest.TestCase):
    def test_unhashed_or_multiple_tool_inputs_refuse(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / 'requirements.txt'
            for value in ['meson==1.12.1\n', REQUIREMENTS.read_text() + 'another==1\n']:
                path.write_text(value)
                with self.assertRaises(ValueError):
                    M.pin(path)

    def test_installed_other_version_never_gets_a_receipt(self):
        with tempfile.TemporaryDirectory() as temporary, patch('subprocess.check_output', return_value='1.7.0\n'):
            output = Path(temporary) / 'receipt.json'
            with self.assertRaises(ValueError):
                M.receipt(sys.executable, REQUIREMENTS, output)
            self.assertFalse(output.exists())

    def test_btbn_image_installs_only_the_shared_hashed_wheel(self):
        with tempfile.TemporaryDirectory() as temporary:
            recipe = Path(temporary); base = recipe / 'images/base'; base.mkdir(parents=True)
            dockerfile = base / 'Dockerfile'
            dockerfile.write_text('FROM debian\nRUN python3 -m pip install --break-system-packages meson\n')
            M.recipe(recipe, REQUIREMENTS)
            self.assertEqual((base / 'portico-meson-requirements.txt').read_bytes(), REQUIREMENTS.read_bytes())
            self.assertIn('--require-hashes --only-binary=:all: --no-deps', dockerfile.read_text())
            self.assertIn('portico-meson-tool.json', dockerfile.read_text())
            with self.assertRaises(ValueError):
                M.recipe(recipe, REQUIREMENTS)

    def test_actual_hashed_wheel_install_and_installed_receipt(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); env = root / 'python'
            subprocess.run([sys.executable, '-m', 'venv', str(env)], check=True, capture_output=True)
            python = env / ('Scripts/python.exe' if sys.platform == 'win32' else 'bin/python')
            subprocess.run([str(python), '-m', 'pip', 'install', '--disable-pip-version-check',
                '--require-hashes', '--only-binary=:all:', '--no-deps', '-r', str(REQUIREMENTS)], check=True, capture_output=True)
            output = root / 'receipt.json'; M.receipt(python, REQUIREMENTS, output)
            data = json.loads(output.read_text())
            self.assertEqual(data['installedVersion'], '1.12.1')
            self.assertEqual(data['wheelSHA256'], M.pin(REQUIREMENTS)['wheelSHA256'])

    def test_native_stage_timing_keeps_build_and_completion_behaviour(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            script = root / 'build-ffmpeg'
            script.write_text('build() {\n return 0\n}\nbuild_done() {\n :\n}\n'
                              'build test-library 1; build_done test-library 1\n')
            M.macos_timing(root)
            import os
            log = root / 'timing.tsv'
            subprocess.run(['bash', str(script)], env=dict(os.environ, PORTICO_ENGINE_TIMINGS=str(log)), check=True)
            rows = [line.split('\t') for line in log.read_text().splitlines()]
            self.assertEqual([x[:2] for x in rows], [['start', 'test-library'], ['end', 'test-library']])
            self.assertGreaterEqual(int(rows[1][2]), int(rows[0][2]))


if __name__ == '__main__':
    unittest.main()
