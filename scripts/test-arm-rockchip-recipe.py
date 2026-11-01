#!/usr/bin/env python3
"""Hosted source-only proof using the actual pinned BtbN Dockerfile generator."""
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tempfile
import unittest
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


@unittest.skipUnless(os.environ.get('PORTICO_NETWORK_BUILD_TOOL_CONTROL') == '1',
                     'Explicit hosted pinned-source intake control required')
class RockchipGraph(unittest.TestCase):
    def test_real_generated_arm_graph_builds_and_copies_both_required_stages(self):
        lock = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())
        with tempfile.TemporaryDirectory(dir=os.environ.get('RUNNER_TEMP')) as temporary:
            work = Path(temporary); pristine = work / 'btbn'; pristine.mkdir()
            def git(*args):
                subprocess.run(['git', '-C', str(pristine), *args], check=True,
                               stdout=subprocess.DEVNULL, timeout=90)
            git('init', '--quiet')
            git('remote', 'add', 'origin', lock['btbn']['repository'])
            git('fetch', '--quiet', '--depth=1', 'origin', lock['btbn']['commit'])
            git('checkout', '--quiet', '--detach', 'FETCH_HEAD')
            actual = subprocess.check_output(['git', '-C', str(pristine), 'rev-parse', 'HEAD'], text=True).strip()
            self.assertEqual(actual, lock['btbn']['commit'])
            archive = work / 'jellyfin-source.tar.gz'
            total = 0
            with urllib.request.urlopen(lock['ffmpeg']['source'], timeout=60) as source, archive.open('wb') as out:
                while data := source.read(1024**2):
                    total += len(data)
                    if total > 64 * 1024**2:
                        raise ValueError('Pinned source control exceeds its archive bound')
                    out.write(data)
            with archive.open('rb') as source:
                self.assertEqual(hashlib.file_digest(source, 'sha256').hexdigest(), lock['ffmpeg']['sha256'])
            unpack = work / 'unpack'; unpack.mkdir()
            with tarfile.open(archive) as source:
                source.extractall(unpack, filter='data')
            roots = list(unpack.iterdir()); self.assertEqual(len(roots), 1)
            env = dict(os.environ, PORTICO_ENGINE_REPOSITORY='portico/source-only-control',
                       PORTICO_DISC_SOURCE_COMMIT=lock['discHelper']['sourceCommit'],
                       SOURCE_DATE_EPOCH=str(lock['sourceDateEpoch']))
            # This is source graph generation only: never Docker, compilation,
            # an engine receipt or a fabricated GitHub execution context.
            def generate(recipe, target):
                subprocess.run(['bash', 'generate.sh', target, 'gpl', '8.1'], cwd=recipe,
                               env=env, check=True, stdout=subprocess.DEVNULL, timeout=90)
                return (recipe / 'Dockerfile').read_text()
            for target, native in [('linux-arm64', 'linuxarm64'), ('windows-x64', 'win64')]:
                recipe = work / target; shutil.copytree(pristine, recipe)
                subprocess.run(['python3', ROOT / 'scripts/configure-ffmpeg-recipe.py', recipe, roots[0], target],
                               env=env, check=True, timeout=60)
                graph = generate(recipe, native)
                for stage in ('rkmpp', 'rkrga'):
                    if target == 'linux-arm64':
                        self.assertRegex(graph, r'(?m)^FROM .+ AS ' + stage + r'$')
                        self.assertIn('src=scripts.d/50-' + stage + '.sh,dst=/stage.sh', graph)
                        self.assertIn('COPY --link --from=' + stage + ' $FFBUILD_DESTPREFIX/. $FFBUILD_PREFIX', graph)
                    else:
                        self.assertNotRegex(graph, r'(?m)^FROM .+ AS ' + stage + r'$')
                if target == 'linux-arm64':
                    final = recipe / 'scripts.d/zz-final.sh'
                    final.write_text(final.read_text().replace('    echo rkmpp\n', '').replace('    echo rkrga\n', ''))
                    omitted = generate(recipe, native)
                    self.assertNotIn(' AS rkmpp\n', omitted)
                    self.assertNotIn(' AS rkrga\n', omitted)


if __name__ == '__main__':
    unittest.main()
