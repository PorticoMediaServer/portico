#!/usr/bin/env python3
"""Expanded feature contract and actual pinned source/configure generation, no engine build."""
import importlib.util
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import unittest
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
LIBRARIES = 'zlib iconv lzma libxml2 libmp3lame libvorbis libvpx libwebp libopenmpt chromaprint'.split()
PLATFORMS = {'linux-amd64', 'linux-arm64', 'windows-amd64', 'darwin-amd64', 'darwin-arm64'}


def upstream(repository, commit, path):
    with urllib.request.urlopen('https://raw.githubusercontent.com/' + repository + '/' + commit + '/' + path, timeout=45) as response:
        return response.read(1024**2).decode()


class Contract(unittest.TestCase):
    def test_same_stem_c_shadow_is_refused_before_source_reuse_or_compilation(self):
        spec = importlib.util.spec_from_file_location('prepare', ROOT / 'scripts/prepare-ffmpeg-source.py')
        module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); (root / 'libavfilter').mkdir()
            (root / 'libavfilter/filter.m').write_text('Objective-C source')
            (root / 'libavfilter/filter.c').touch()
            with self.assertRaisesRegex(ValueError, 'C source shadows Objective-C'):
                module.check_objective_c_sources(root)
            result = subprocess.run([sys.executable, ROOT / 'scripts/macos-ffmpeg-source.py',
                'check-source', '--recipe', root, '--source', root], capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn('C source shadows Objective-C', result.stderr)
            (root / 'libavfilter/filter.c').unlink()
            module.check_objective_c_sources(root)

    def test_requirements_cover_libraries_and_their_real_runtime_inventories(self):
        features = {r['id']: r for r in json.loads((ROOT / 'third_party/ffmpeg/requirements.v1.json').read_text())['features']}
        for name, kind, values in [('library.formats', 'library', LIBRARIES), ('encode.mp3', 'encoder', ['libmp3lame']),
                ('container.format-libraries', 'demuxer', ['dash', 'libopenmpt']),
                ('fingerprint.chromaprint', 'muxer', ['chromaprint']), ('library.srt', 'library', ['libsrt']),
                ('protocol.srt', 'protocol', ['srt'])]:
            row = features[name]
            self.assertEqual(row['kind'], kind)
            self.assertEqual(set(row['acceptable']), set(values))
            self.assertTrue(row['required']); self.assertNotEqual(row.get('match'), 'any')
            self.assertEqual(set(row['platforms']), PLATFORMS - ({'windows-amd64'} if name.endswith('.srt') else set()))

    def test_native_linux_roots_and_explicit_terminal_flags_cover_new_contract(self):
        lock = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())
        self.assertTrue({'libvorbis', 'libwebp', 'openmpt', 'chromaprint', 'srt'} <= set(lock['linuxSourceBuild']['roots']))
        terminal = (ROOT / 'scripts/build-linux-ffmpeg.py').read_text()
        for library in LIBRARIES + ['libsrt']:
            self.assertIn('--enable-' + library, terminal)
        extras = ROOT / lock['macosRecipe']['extrasLock']['path']
        self.assertEqual(hashlib.sha256(extras.read_bytes()).hexdigest(), lock['macosRecipe']['extrasLock']['sha256'])

    @unittest.skipUnless(os.environ.get('PORTICO_NETWORK_BUILD_TOOL_CONTROL') == '1', 'explicit hosted pinned source/configure graph control')
    def test_all_six_real_recipe_configurations_and_four_existing_source_pins(self):
        lock = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())
        extras = json.loads((ROOT / 'third_party/ffmpeg/macos-extras.lock.json').read_text())
        for name in ('xz', 'openmpt', 'chromaprint', 'srt'):
            pin = extras[name]; authority = pin['sourceAuthority']
            self.assertEqual(authority['commit'], lock['btbn']['commit'])
            raw = upstream('BtbN/FFmpeg-Builds', authority['commit'], authority['script'])
            self.assertIn('SCRIPT_REPO="' + pin['repository'] + '"', raw)
            self.assertIn('SCRIPT_COMMIT="' + pin['commit'] + '"', raw)
        with tempfile.TemporaryDirectory(dir=os.environ.get('RUNNER_TEMP')) as temporary:
            work = Path(temporary)
            source = work / 'jellyfin/builder/scripts.d'; source.mkdir(parents=True)
            for name in ('50-rkmpp.sh', '50-rkrga.sh'):
                (source / name).write_text(upstream('jellyfin/jellyfin-ffmpeg', lock['ffmpeg']['commit'], 'builder/scripts.d/' + name))
            pristine = work / 'btbn'; pristine.mkdir()
            for args in (('init', '--quiet'), ('remote', 'add', 'origin', lock['btbn']['repository']),
                    ('fetch', '--quiet', '--depth=1', 'origin', lock['btbn']['commit']), ('checkout', '--quiet', '--detach', 'FETCH_HEAD')):
                subprocess.run(['git', '-C', pristine, *args], check=True, timeout=90)
            env = dict(os.environ, PORTICO_DISC_SOURCE_COMMIT=lock['discHelper']['sourceCommit'],
                       SOURCE_DATE_EPOCH=str(lock['sourceDateEpoch']))
            for target, native in [('linux-x64', 'linux64'), ('linux-arm64', 'linuxarm64'),
                    ('windows-x64', 'win64'), ('windows-arm64', 'winarm64'), ('macos-arm64', None), ('macos-x64', None)]:
                recipe = work / target
                if native:
                    shutil.copytree(pristine, recipe)
                else:
                    recipe.mkdir()
                    (recipe / 'build-ffmpeg').write_text(upstream('homebridge/ffmpeg-for-homebridge', lock['macosRecipe']['commit'], 'build-ffmpeg'))
                    for name in ('homebridge-sourceforge-download.patch', 'homebridge-gpl-only.patch', 'homebridge-arm64-vpx.patch',
                                 'homebridge-private-python.patch', 'homebridge-zix-build.patch', 'homebridge-private-pkgconfig.patch'):
                        subprocess.run(['git', 'apply', ROOT / 'third_party/ffmpeg/patches' / name], cwd=recipe, check=True, capture_output=True)
                    if target == 'macos-x64':
                        subprocess.run(['git', 'apply', ROOT / 'third_party/ffmpeg/patches/homebridge-intel-gmp-pic.patch'], cwd=recipe, check=True, capture_output=True)
                result = subprocess.run([sys.executable, ROOT / 'scripts/configure-ffmpeg-recipe.py', recipe, work / 'jellyfin', target],
                                        env=env, capture_output=True, text=True, timeout=60)
                self.assertEqual(result.returncode, 0, result.stderr)
                if native:
                    subprocess.run(['bash', 'generate.sh', native, 'gpl', '8.1'], cwd=recipe, env=env, check=True,
                                   timeout=60, stdout=subprocess.DEVNULL)
                    flags = (recipe / 'build.sh').read_text()
                else:
                    flags = (recipe / 'build-ffmpeg').read_text().split('build "ffmpeg" "${FFMPEG_VERSION}"')[0].rstrip().splitlines()[-1]
                    subprocess.run(['bash', '-n', recipe / 'portico-macos-extras.sh'], check=True)
                    self.assertTrue((recipe / 'portico-macos-system-libs.py').is_file())
                for library in LIBRARIES + ([] if target.startswith('windows') else ['libsrt']):
                    if library == 'libopenmpt' and target == 'windows-arm64': continue
                    self.assertIn('--enable-' + library, flags, target)
                self.assertNotIn('--enable-libfdk-aac', flags)
                if target.startswith('windows'): self.assertNotIn('--enable-libsrt', flags)
                print('actual format flags checked: ' + target, flush=True)


if __name__ == '__main__':
    unittest.main()
