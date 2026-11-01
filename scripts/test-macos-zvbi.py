#!/usr/bin/env python3
"""Source/tool pin refusal controls; real library proof runs on native Mac."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class MacZvbi(unittest.TestCase):
    def test_extra_source_lock_is_sealed_by_the_main_engine_lock(self):
        lock = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())
        entry = lock['macosRecipe']['extrasLock']
        self.assertEqual(hashlib.sha256((ROOT / entry['path']).read_bytes()).hexdigest(), entry['sha256'])
        extras = json.loads((ROOT / entry['path']).read_text())
        self.assertEqual(extras['zvbi']['commit'], 'd3a5ee9f2b047bf16cd1ee5ccf6ec05ee75409d0')
        self.assertEqual(extras['gettextTools']['version'], '0.26')

    def test_changed_cached_gettext_source_refuses_before_any_build_or_network(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); packages = root / 'packages'; packages.mkdir()
            archive = packages / 'portico-gettext-source.tar.xz'; archive.write_bytes(b'wrong source')
            lock = root / 'extras.json'; lock.write_text(json.dumps({'gettextTools': {
                'sha256': '0' * 64, 'url': 'https://example.invalid/must-not-be-fetched'}}))
            shell = 'set -e; portico_clone(){ exit 98; }; portico_lock="$1"; PACKAGES="$2"; CWD="$3"; WORKSPACE="$3/prefix"; source "$4"'
            result = subprocess.run(['bash', '-c', shell, 'control', str(lock), str(packages), str(root),
                str(ROOT / 'scripts/build-macos-zvbi.sh')], capture_output=True, text=True, env=os.environ, timeout=20)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('Checked gettext source changed', result.stderr)
            self.assertEqual(archive.read_bytes(), b'wrong source')


if __name__ == '__main__':
    unittest.main()
