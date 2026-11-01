#!/usr/bin/env python3
"""Exercise actual pinned Mac recipe cache hooks without compiling libraries."""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('mac_cache', ROOT / 'scripts/macos-dependency-cache.py')
M = importlib.util.module_from_spec(spec); spec.loader.exec_module(M)


class MacCache(unittest.TestCase):
    def test_disabled_adapter_does_not_touch_recipe(self):
        with patch.dict(os.environ, {}, clear=True): M.configure(Path('/does-not-exist'))

    @unittest.skipUnless(os.environ.get('PORTICO_NETWORK_BUILD_TOOL_CONTROL') == '1', 'hosted exact pinned Mac recipe control')
    def test_real_dependency_hooks_keep_final_flags_out_and_propagate_failures(self):
        lock = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())
        url = 'https://raw.githubusercontent.com/homebridge/ffmpeg-for-homebridge/' + lock['macosRecipe']['commit'] + '/build-ffmpeg'
        with urllib.request.urlopen(url, timeout=30) as response: raw = response.read(1024**2)
        self.assertEqual(hashlib.sha256(raw).hexdigest(), '285a599952b4e42b92ae92685d762c3232464c44148ed57d47a65f234b1ac313')
        plans = []
        with tempfile.TemporaryDirectory() as temporary:
            base = Path(temporary)
            for index in (1, 2):
                recipe = base / str(index); recipe.mkdir()
                (recipe / 'build-ffmpeg').write_bytes(raw)
                for name in ('homebridge-sourceforge-download.patch', 'homebridge-gpl-only.patch', 'homebridge-arm64-vpx.patch',
                             'homebridge-private-python.patch', 'homebridge-zix-build.patch', 'homebridge-private-pkgconfig.patch'):
                    subprocess.run(['git', 'apply', str(ROOT / 'third_party/ffmpeg/patches' / name)], cwd=recipe, check=True, capture_output=True)
                subprocess.run([sys.executable, ROOT / 'scripts/configure-ffmpeg-recipe.py', recipe, base, 'macos-arm64'],
                               check=True, capture_output=True)
                if index == 2:
                    text = (recipe / 'build-ffmpeg').read_text().replace('"--enable-libdav1d"', '"--enable-libdav1d" "--enable-fixture-final-only"')
                    (recipe / 'build-ffmpeg').write_text(text)
                    (recipe / 'packaging-only').write_text('irrelevant installer change')
                env = dict(PORTICO_ENGINE_STAGE_CACHE=str(base / 'cache'), PORTICO_FFMPEG_TARGET='macos-arm64',
                           PORTICO_ENGINE_MAC_COMPILER=json.dumps({'fixture': 'inert compiler identity'}))
                with patch.dict(os.environ, env): M.configure(recipe)
                for name in ('build-ffmpeg', 'portico-macos-extras.sh'):
                    subprocess.run(['bash', '-n', recipe / name], check=True, capture_output=True)
                plan = json.loads((recipe / 'portico-dependency-cache.json').read_text())
                self.assertIn('cmake', plan['stages']); self.assertIn('portico-moltenVK', plan['stages'])
                self.assertNotIn('ffmpeg', plan['stages'])
                self.assertIn('ffmpeg.done', (recipe / 'build-ffmpeg').read_text())
                self.assertIn('finish "$1" "$2"', (recipe / 'build-ffmpeg').read_text())
                plans.append(plan)
            self.assertEqual(plans[0], plans[1])
        body = 'false\necho incorrectly-continued\n'
        script = 'set -e\nportico_dependency_cache(){ echo MISS; }\n' + M.wrapper('fixture', body)
        result = subprocess.run(['bash', '-c', script], capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn('incorrectly-continued', result.stdout)


if __name__ == '__main__': unittest.main()
