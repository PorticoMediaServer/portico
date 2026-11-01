#!/usr/bin/env python3
"""Prepared-source and shell-boundary controls; no library/engine compilation."""
import hashlib
import io
import json
import os
from pathlib import Path
import runpy
import shutil
import subprocess
import sys
import tarfile
import tempfile
import unittest
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
M = runpy.run_path(str(ROOT / 'scripts/macos-ffmpeg-source.py'))


class MacPreparedSource(unittest.TestCase):
    def recipe(self, root):
        recipe = root / 'recipe'; recipe.mkdir()
        shutil.copyfile(ROOT / 'third_party/ffmpeg/sources.lock.json', recipe / 'portico-ffmpeg-sources.lock.json')
        return recipe

    def archive(self, root):
        path = root / 'prepared.tar.xz'
        with tarfile.open(path, 'w:xz') as out:
            data = b'controlled already-patched source fixture, not an engine\n'
            info = tarfile.TarInfo('ffmpeg/portico-post-series-sentinel'); info.size = len(data)
            out.addfile(info, io.BytesIO(data))
        return path

    def test_seed_copies_the_exact_prepared_archive_to_a_readonly_content_addressed_input(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); recipe = self.recipe(root); source = self.archive(root)
            name = M['seed'](recipe, source)
            self.assertIn(hashlib.sha256(source.read_bytes()).hexdigest(), name)
            self.assertEqual((recipe / 'packages' / name).read_bytes(), source.read_bytes())
            self.assertEqual((recipe / 'packages' / name).stat().st_mode & 0o777, 0o444)
            self.assertEqual(M['verify'](recipe), name)
            record = json.loads((recipe / 'portico-ffmpeg-source.json').read_text())
            self.assertIs(record['release'], False)

    def test_missing_changed_linked_source_and_changed_contract_refuse_without_a_fallback(self):
        for mutation in ('missing', 'changed', 'linked', 'contract'):
            with self.subTest(mutation=mutation), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary); recipe = self.recipe(root); source = self.archive(root)
                name = M['seed'](recipe, source); path = recipe / 'packages' / name
                if mutation in ('missing', 'linked'):
                    path.unlink()
                    if mutation == 'linked':
                        path.symlink_to(source)
                elif mutation == 'changed':
                    path.chmod(0o644); path.write_bytes(b'unpatched replacement')
                else:
                    (recipe / 'portico-ffmpeg-sources.lock.json').write_text('{}')
                with self.assertRaises(ValueError):
                    M['verify'](recipe)

    def test_extras_strict_options_and_cwd_do_not_leak_but_a_real_failure_still_stops_the_parent(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); recipe = self.recipe(root)
            extra = recipe / 'portico-macos-extras.sh'
            extra.write_text('set -euo pipefail\ncd /\nexport PORTICO_CONTROL_LEAK=leaked\n')
            prefix = 'set +u; CWD="$1"; original=$PWD\n'
            suffix = '\n[ "$PWD" = "$original" ]; [ "${PORTICO_CONTROL_LEAK:-}" = "" ]; optional(){ printf "%s" "$3"; }; optional one two; echo parent-continued\n'
            result = subprocess.run(['bash', '-c', prefix + M['extras_block']() + suffix, 'fixture', str(recipe)],
                                    capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn('parent-continued', result.stdout)
            # Reproduce the old sourcing bug using the exact same fixture.
            old = subprocess.run(['bash', '-c', prefix + 'source "$CWD/portico-macos-extras.sh"\noptional(){ printf "%s" "$3"; }; optional one two\n',
                                  'fixture', str(recipe)], capture_output=True, text=True, timeout=10)
            self.assertNotEqual(old.returncode, 0)
            self.assertIn('unbound variable', old.stderr)
            extra.write_text('set -euo pipefail\nfalse\necho failure-was-ignored\n')
            failed = subprocess.run(['bash', '-c', prefix + M['extras_block']() + '\necho parent-continued\n',
                                     'fixture', str(recipe)], capture_output=True, text=True, timeout=10)
            self.assertNotEqual(failed.returncode, 0)
            self.assertNotIn('failure-was-ignored', failed.stdout)
            self.assertNotIn('parent-continued', failed.stdout)

    @unittest.skipUnless(os.environ.get('PORTICO_NETWORK_BUILD_TOOL_CONTROL') == '1', 'hosted pinned-recipe source control')
    def test_actual_pinned_recipe_configurator_and_download_consume_only_the_prepared_sentinel(self):
        lock = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())
        url = 'https://raw.githubusercontent.com/homebridge/ffmpeg-for-homebridge/' + lock['macosRecipe']['commit'] + '/build-ffmpeg'
        with urllib.request.urlopen(url, timeout=30) as response:
            raw = response.read(1024**2)
        self.assertEqual(hashlib.sha256(raw).hexdigest(), '285a599952b4e42b92ae92685d762c3232464c44148ed57d47a65f234b1ac313')
        text = raw.decode()
        actual_download = text[text.index('download() {'):text.index('execute() {')]
        actual_mkdir = text[text.index('make_dir() {'):text.index('clone() {')]
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); recipe = self.recipe(root); source = self.archive(root)
            (recipe / 'build-ffmpeg').write_bytes(raw)
            for patch in ('homebridge-sourceforge-download.patch', 'homebridge-gpl-only.patch',
                          'homebridge-arm64-vpx.patch', 'homebridge-private-python.patch',
                          'homebridge-zix-build.patch', 'homebridge-private-pkgconfig.patch'):
                subprocess.run(['git', 'apply', str(ROOT / 'third_party/ffmpeg/patches' / patch)],
                               cwd=recipe, check=True, capture_output=True)
            subprocess.run([sys.executable, ROOT / 'scripts/configure-ffmpeg-recipe.py', recipe, root, 'macos-arm64'],
                           check=True, capture_output=True)
            configured = (recipe / 'build-ffmpeg').read_text()
            final = configured.split('build "ffmpeg" "${FFMPEG_VERSION}"', 1)[1].split('# shellcheck', 1)[0]
            self.assertNotIn('ffmpeg.org', final)
            self.assertNotIn('FFmpeg-release-', final)
            name = M['seed'](recipe, source)
            prefix = ('set -eu\nCWD="$1"; PACKAGES="$CWD/packages"\n'
                      'curl(){ echo unexpected-source-download >&2; return 91; }\n')
            result = subprocess.run(['bash', '-c', prefix + actual_mkdir + actual_download + final +
                '\ncat portico-post-series-sentinel\n', 'fixture', str(recipe)], capture_output=True, text=True, timeout=30)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn('controlled already-patched source fixture', result.stdout)
            self.assertNotIn('unexpected-source-download', result.stderr)
            (recipe / 'packages' / name).unlink()
            refused = subprocess.run(['bash', '-c', prefix + actual_mkdir + actual_download + final,
                                      'fixture', str(recipe)], capture_output=True, text=True, timeout=30)
            self.assertNotEqual(refused.returncode, 0)
            self.assertNotIn('unexpected-source-download', refused.stderr)
            self.assertIn('no upstream fallback', refused.stderr)


if __name__ == '__main__':
    unittest.main()
