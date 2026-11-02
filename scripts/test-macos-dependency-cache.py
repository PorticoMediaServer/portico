#!/usr/bin/env python3
"""Exercise actual pinned Mac recipe cache hooks without compiling libraries."""
import hashlib
import importlib.util
import json
import os
import re
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

    def test_consumed_disc_patches_are_bound_to_their_reviewed_pins(self):
        lock = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())
        with tempfile.TemporaryDirectory() as temporary:
            recipe = Path(temporary); patches = recipe / 'portico-no-decryption'; patches.mkdir()
            for pin in lock['linuxSourceBuild']['sourcePatches']:
                (patches / Path(pin['path']).name).write_bytes((ROOT / pin['path']).read_bytes())
            self.assertEqual(M.disc_patch_inputs(recipe), {Path(p['path']).name: p['sha256'] for p in lock['linuxSourceBuild']['sourcePatches']})
            path = next(patches.iterdir()); path.write_bytes(path.read_bytes() + b'changed')
            with self.assertRaisesRegex(ValueError, 'dependency patch changed'): M.disc_patch_inputs(recipe)

    @unittest.skipUnless(os.environ.get('PORTICO_NETWORK_BUILD_TOOL_CONTROL') == '1', 'hosted exact pinned Mac recipe control')
    def test_real_dependency_hooks_keep_final_flags_out_and_propagate_failures(self):
        lock = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())
        url = 'https://raw.githubusercontent.com/homebridge/ffmpeg-for-homebridge/' + lock['macosRecipe']['commit'] + '/build-ffmpeg'
        with urllib.request.urlopen(url, timeout=30) as response: raw = response.read(1024**2)
        self.assertEqual(hashlib.sha256(raw).hexdigest(), '285a599952b4e42b92ae92685d762c3232464c44148ed57d47a65f234b1ac313')
        plans = []
        with tempfile.TemporaryDirectory() as temporary:
            base = Path(temporary)
            for index in (1, 2, 3):
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
                if index == 3:
                    path = recipe / 'portico-macos-extras.sh'
                    text, count = re.subn(r'(?ms)^# Retry only this stage.*?^fi\n',
                                         'git submodule update --init --recursive\n', path.read_text())
                    self.assertEqual(count, 1); path.write_text(text)
                env = dict(PORTICO_ENGINE_STAGE_CACHE=str(base / 'cache'), PORTICO_FFMPEG_TARGET='macos-arm64',
                           PORTICO_ENGINE_MAC_COMPILER=json.dumps({'fixture': 'inert compiler identity'}))
                with patch.dict(os.environ, env): M.configure(recipe)
                for name in ('build-ffmpeg', 'portico-macos-extras.sh'):
                    subprocess.run(['bash', '-n', recipe / name], check=True, capture_output=True)
                plan = json.loads((recipe / 'portico-dependency-cache.json').read_text())
                self.assertEqual(plan['context']['dependencyPatches'], M.disc_patch_inputs(recipe))
                self.assertIn('cmake', plan['stages']); self.assertIn('portico-moltenVK', plan['stages'])
                self.assertNotIn('ffmpeg', plan['stages'])
                self.assertIn('ffmpeg.done', (recipe / 'build-ffmpeg').read_text())
                self.assertIn('finish "$1" "$2"', (recipe / 'build-ffmpeg').read_text())
                plans.append(plan)
            self.assertEqual(plans[0], plans[1])
            self.assertEqual(plans[0]['context'], plans[2]['context'])
            changed = [name for name in plans[0]['stages'] if plans[0]['stages'][name] != plans[2]['stages'][name]]
            self.assertEqual(changed, ['portico-placebo'])
            print('Pinned Mac stage-input proof: only placebo changes; all preceding stage inputs remain identical', flush=True)
        body = 'false\necho incorrectly-continued\n'
        script = 'set -e\nportico_dependency_cache(){ echo MISS; }\n' + M.wrapper('fixture', body)
        result = subprocess.run(['bash', '-c', script], capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn('incorrectly-continued', result.stdout)

    def test_actual_placebo_retry_is_bounded_and_propagates_final_failure(self):
        text = (ROOT / 'scripts/build-macos-ffmpeg-extras.sh').read_text()
        body = text.split('portico_clone placebo\n', 1)[1].split('portico_meson ', 1)[0]
        for failures, expected, sleeps in ((0, 0, []), (2, 0, ['10', '20']), (99, 128, ['10', '20', '40', '50'])):
            setup = ('set -eu\ncalls=0\ngit(){ [[ "$*" == "submodule update --init --recursive" ]] || exit 91; '
                     'calls=$((calls+1)); echo "call $calls"; [[ "$calls" -gt ' + str(failures) + ' ]] || return 128; }\n'
                     'sleep(){ echo "delay $1"; }\n')
            result = subprocess.run(['/bin/bash', '-c', setup + body + '\necho continued\n'], text=True, capture_output=True)
            self.assertEqual(result.returncode, expected, result.stderr)
            self.assertEqual([line[6:] for line in result.stdout.splitlines() if line.startswith('delay ')], sleeps)
            self.assertEqual(sum(line.startswith('call ') for line in result.stdout.splitlines()), min(failures + 1, 5))
            self.assertEqual('continued' in result.stdout, expected == 0)


if __name__ == '__main__': unittest.main()
