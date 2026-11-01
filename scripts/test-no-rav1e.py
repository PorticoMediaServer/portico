#!/usr/bin/env python3
"""Actual pinned recipe generation: no rav1e, SVT-AV1/libdovi retained."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import unittest
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


class ActiveContract(unittest.TestCase):
    def test_rav1e_resolution_and_vendor_builder_are_removed_from_the_active_contract(self):
        lock = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())
        self.assertNotIn('rav1eCargo', lock['btbn'])
        for path in ('scripts/configure-windows-rav1e.py', 'scripts/test-windows-rav1e-custody.py',
                     'third_party/ffmpeg/locks/rav1e-Cargo.lock'):
            self.assertFalse((ROOT / path).exists(), path)
        requirements = (ROOT / 'third_party/ffmpeg/requirements.v1.json').read_text()
        self.assertNotIn('rav1e', requirements)
        self.assertIn('libsvtav1', requirements)
        self.assertIn('libdovi', (ROOT / 'scripts/configure-ffmpeg-recipe.py').read_text())
        self.assertIn('SKIPRAV1E=yes', (ROOT / 'scripts/build-ffmpeg.sh').read_text())


@unittest.skipUnless(os.environ.get('PORTICO_NETWORK_BUILD_TOOL_CONTROL') == '1',
                     'Explicit hosted actual pinned-recipe generation control')
class GeneratedTargets(unittest.TestCase):
    def test_all_four_btbn_and_both_mac_configurations_keep_svt_and_reject_rav1e(self):
        lock = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())
        with tempfile.TemporaryDirectory(dir=os.environ.get('RUNNER_TEMP')) as temporary:
            work = Path(temporary)
            def clone(entry, destination):
                destination.mkdir()
                for args in (('init', '--quiet'), ('remote', 'add', 'origin', entry['repository']),
                             ('fetch', '--quiet', '--depth=1', 'origin', entry['commit']),
                             ('checkout', '--quiet', '--detach', 'FETCH_HEAD')):
                    subprocess.run(['git', '-C', destination, *args], check=True, timeout=60,
                                   stdout=subprocess.DEVNULL)
                self.assertEqual(subprocess.check_output(['git', '-C', destination, 'rev-parse', 'HEAD'],
                                                          text=True).strip(), entry['commit'])
            pristine = work / 'btbn'; clone(lock['btbn'], pristine)
            archive = work / 'jellyfin.tar.gz'
            with urllib.request.urlopen(lock['ffmpeg']['source'], timeout=60) as inp, archive.open('wb') as out:
                total = 0
                while data := inp.read(1024**2):
                    total += len(data)
                    if total > 64 * 1024**2:
                        raise ValueError('pinned source archive exceeds control bound')
                    out.write(data)
            with archive.open('rb') as inp:
                self.assertEqual(hashlib.file_digest(inp, 'sha256').hexdigest(), lock['ffmpeg']['sha256'])
            unpack = work / 'source'; unpack.mkdir()
            with tarfile.open(archive) as inp:
                inp.extractall(unpack, filter='data')
            roots = list(unpack.iterdir()); self.assertEqual(len(roots), 1)
            env = dict(os.environ, PORTICO_DISC_SOURCE_COMMIT=lock['discHelper']['sourceCommit'],
                       SOURCE_DATE_EPOCH=str(lock['sourceDateEpoch']),
                       PORTICO_ENGINE_REPOSITORY='portico/source-only-control')
            def configure(recipe, target, explain=True):
                result = subprocess.run(['python3', ROOT / 'scripts/configure-ffmpeg-recipe.py', recipe, roots[0], target],
                                        env=env, timeout=60, capture_output=True, text=True)
                if result.returncode and explain:
                    print(result.stderr, flush=True)
                result.check_returncode()
                return result
            def check(flags, target):
                self.assertNotIn('--enable-librav1e', flags, target)
                self.assertIn('--disable-librav1e', flags, target)
                self.assertIn('--enable-libsvtav1', flags, target)
                self.assertIn('--enable-libdovi', flags, target)
                print('actual pinned configure flags checked: ' + target, flush=True)
            for target, native in (('linux-x64', 'linux64'), ('linux-arm64', 'linuxarm64'),
                                   ('windows-x64', 'win64'), ('windows-arm64', 'winarm64')):
                recipe = work / target; shutil.copytree(pristine, recipe)
                configure(recipe, target)
                self.assertFalse((recipe / 'scripts.d/50-rav1e.sh').exists())
                self.assertFalse((recipe / 'portico-rav1e-inputs').exists())
                subprocess.run(['bash', 'generate.sh', native, 'gpl', '8.1'], cwd=recipe,
                               env=env, check=True, timeout=60, stdout=subprocess.DEVNULL)
                graph = (recipe / 'Dockerfile').read_text()
                self.assertNotIn('scripts.d/50-rav1e.sh', graph)
                self.assertNotRegex(graph, r'(?m)^FROM .+ AS rav1e$')
                # Defaults live in the graph environment; Portico's final
                # overrides live in the actual terminal build payload.
                check(graph + '\n' + (recipe / 'build.sh').read_text(), target)
            changed = work / 'changed-stage'; shutil.copytree(pristine, changed)
            stage = changed / 'scripts.d/50-rav1e.sh'; stage.write_text(stage.read_text() + '\n# changed\n')
            with self.assertRaises(subprocess.CalledProcessError) as refused:
                configure(changed, 'linux-x64', explain=False)
            self.assertIn('pinned rav1e removal boundary changed', refused.exception.stderr)
            homebridge = work / 'homebridge'; clone(lock['macosRecipe'], homebridge)
            for target in ('macos-arm64', 'macos-x64'):
                recipe = work / target; shutil.copytree(homebridge, recipe)
                patches = ['homebridge-sourceforge-download.patch', 'homebridge-gpl-only.patch',
                           'homebridge-arm64-vpx.patch', 'homebridge-private-python.patch',
                           'homebridge-zix-build.patch', 'homebridge-private-pkgconfig.patch']
                if target == 'macos-x64':
                    patches.append('homebridge-intel-gmp-pic.patch')
                for name in patches:
                    subprocess.run(['git', 'apply', ROOT / 'third_party/ffmpeg/patches' / name],
                                   cwd=recipe, check=True, timeout=10, capture_output=True)
                configure(recipe, target)
                # Read the actual final options appended by the production
                # configurator, without running a Mac compiler on Linux.
                prefix = (recipe / 'build-ffmpeg').read_text().split('build "ffmpeg" "${FFMPEG_VERSION}"')[0]
                assignment = prefix.rstrip().splitlines()[-1]
                self.assertTrue(assignment.startswith('CONFIGURE_OPTIONS+=('))
                flags = subprocess.check_output(['bash', '-c', 'set -e\nCONFIGURE_OPTIONS=()\n' + assignment +
                    '\nprintf "%s\\n" "${CONFIGURE_OPTIONS[@]}"\n'], text=True)
                check(flags, target)


if __name__ == '__main__':
    unittest.main()
