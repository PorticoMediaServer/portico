#!/usr/bin/env python3
"""Pinned source/hash/graph controls only; never compiles bzip2 or an engine."""
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('bzip2', ROOT / 'scripts/configure-ffmpeg-bzip2.py')
M = importlib.util.module_from_spec(spec); spec.loader.exec_module(M)


class Bzip2(unittest.TestCase):
    def test_static_stage_is_locked_retains_notice_and_never_runs_upstream_all_or_install(self):
        pin = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())['bzip2']
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); (root / 'scripts.d').mkdir()
            (root / 'scripts.d/zz-final.sh').write_text('ffbuild_depends() {\n echo one\n}\n')
            M.configure(root)
            script = root / 'scripts.d/20-portico-bzip2.sh'; text = script.read_text()
            self.assertIn(pin['source'], text); self.assertIn(pin['sha256'], text)
            self.assertLess(text.index('sha256sum -c -'), text.index('tar -xzf'))
            self.assertIn('CFLAGS="$CFLAGS -fPIC" libbz2.a', text)
            self.assertIn('share/licenses/bzip2/LICENSE', text)
            self.assertNotIn('make install', text); self.assertNotIn('make all', text)
            self.assertIn('echo portico-bzip2', (root / 'scripts.d/zz-final.sh').read_text())
            subprocess.run(['bash', '-n', script], check=True)

    @unittest.skipUnless(os.environ.get('PORTICO_NETWORK_BUILD_TOOL_CONTROL') == '1', 'explicit hosted official source hash control')
    def test_actual_official_archive_hash_source_and_licence_without_compiling(self):
        pin = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())['bzip2']
        with urllib.request.urlopen(pin['source'], timeout=45) as response: raw = response.read(2 * 1024**2)
        self.assertEqual(hashlib.sha256(raw).hexdigest(), pin['sha256'])
        with tarfile.open(fileobj=io.BytesIO(raw)) as source:
            prefix = 'bzip2-' + pin['version'] + '/'
            self.assertIn(b'Redistribution and use in source and binary forms', source.extractfile(prefix + 'LICENSE').read())
            for name in ('bzlib.h', 'bzlib.c', 'Makefile'):
                self.assertTrue(source.extractfile(prefix + name).read())


if __name__ == '__main__': unittest.main()
