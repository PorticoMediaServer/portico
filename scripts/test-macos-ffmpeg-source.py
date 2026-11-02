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

    def test_download_pin_is_checked_before_extraction_for_fresh_and_cached_bytes(self):
        with tempfile.TemporaryDirectory() as temporary:
            recipe = self.recipe(Path(temporary)); (recipe / 'packages').mkdir()
            archive = recipe / 'packages/archive.tar.gz'; archive.write_bytes(b'controlled source bytes')
            extras = recipe / 'portico-macos-extras.lock.json'
            url = 'https://zlib.net/fossils/zlib-1.3.1.tar.gz'
            sha = hashlib.sha256(archive.read_bytes()).hexdigest()
            extras.write_text(json.dumps({'archivePins': {url: sha}}))
            authority = {'macosRecipe': {'extrasLock': {'sha256': hashlib.sha256(extras.read_bytes()).hexdigest()}}}
            (recipe / 'portico-ffmpeg-sources.lock.json').write_text(json.dumps(authority))
            self.assertEqual(M['verify_download'](recipe, url, archive), sha)
            archive.write_bytes(b'changed cached or newly downloaded bytes')
            with self.assertRaisesRegex(ValueError, 'checksum mismatch'): M['verify_download'](recipe, url, archive)
            extras.write_text('{}')
            with self.assertRaisesRegex(ValueError, 'sealed source contract'): M['verify_download'](recipe, url, archive)

    @unittest.skipUnless(os.environ.get('PORTICO_NETWORK_BUILD_TOOL_CONTROL') == '1', 'hosted real Homebridge downloader control')
    def test_actual_downloader_retries_same_url_with_bounded_backoff_and_refuses_corruption(self):
        lock = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())
        url = 'https://raw.githubusercontent.com/homebridge/ffmpeg-for-homebridge/' + lock['macosRecipe']['commit'] + '/build-ffmpeg'
        with urllib.request.urlopen(url, timeout=30) as response: raw = response.read(1024**2).decode()
        configured = M['download_retries'](raw)
        download = configured[configured.index('download() {'):configured.index('execute() {')]
        for case, failures, expected_calls in (('recover', 2, 3), ('exhaust', 99, 5), ('corrupt', 0, 1), ('cached', 99, 0)):
            with self.subTest(case=case), tempfile.TemporaryDirectory() as temporary:
                recipe = self.recipe(Path(temporary)); (recipe / 'packages').mkdir()
                shutil.copyfile(ROOT / 'scripts/macos-ffmpeg-source.py', recipe / 'portico-macos-ffmpeg-source.py')
                address = 'https://example.invalid/locked-fixture.tar.gz'
                extras = recipe / 'portico-macos-extras.lock.json'
                extras.write_text(json.dumps({'archivePins': {address: hashlib.sha256(b'inert downloaded source').hexdigest()}}))
                authority = {'macosRecipe': {'extrasLock': {'sha256': hashlib.sha256(extras.read_bytes()).hexdigest()}}}
                (recipe / 'portico-ffmpeg-sources.lock.json').write_text(json.dumps(authority))
                fake = recipe / 'fixture-curl.py'
                fake.write_text("""import json,os,pathlib,sys
root=pathlib.Path(os.environ['PORTICO_FIXTURE_ROOT']); log=root/'calls.json'
calls=json.loads(log.read_text()) if log.exists() else []
calls.append(sys.argv[1:]);log.write_text(json.dumps(calls))
output=pathlib.Path(sys.argv[sys.argv.index('-o')+1]);output.write_bytes(b'partial fixture')
if len(calls)<=int(os.environ['PORTICO_FIXTURE_FAILURES']):sys.exit(6)
output.write_bytes(b'corrupt fixture' if os.environ['PORTICO_FIXTURE_CASE']=='corrupt' else b'inert downloaded source')
""")
                if case == 'cached': (recipe / 'packages/fixture.tar.gz').write_bytes(b'corrupt cached input')
                prefix = ('set -e; CWD="$1"; PACKAGES="$CWD/packages"\n'
                          'make_dir(){ mkdir -p "$1"; }\n'
                          'curl(){ python3 "$CWD/fixture-curl.py" "$@"; }\n'
                          'sleep(){ echo "$1" >> "$CWD/sleeps"; }\n'
                          'tar(){ echo extracted >> "$CWD/extractions"; }\n')
                env = dict(os.environ, PORTICO_FIXTURE_ROOT=str(recipe), PORTICO_FIXTURE_FAILURES=str(failures), PORTICO_FIXTURE_CASE=case)
                result = subprocess.run(['bash', '-c', prefix + download + '\ndownload "' + address + '" fixture.tar.gz ""\n',
                    'fixture', str(recipe)], capture_output=True, text=True, timeout=20, env=env)
                calls = json.loads((recipe / 'calls.json').read_text()) if (recipe / 'calls.json').exists() else []
                self.assertEqual(len(calls), expected_calls, result.stderr)
                self.assertTrue(all(call[-1] == address for call in calls))
                self.assertTrue(all('--max-time' in call and call[call.index('--max-time')+1] == '15' for call in calls))
                waits = (recipe / 'sleeps').read_text().splitlines() if (recipe / 'sleeps').exists() else []
                self.assertEqual(waits, ['5', '10'] if case == 'recover' else ['5', '10', '15', '20'] if case == 'exhaust' else [])
                self.assertEqual(result.returncode == 0, case == 'recover', result.stderr)
                self.assertEqual((recipe / 'extractions').exists(), case == 'recover')
                if case in ('corrupt', 'cached'): self.assertIn('checksum mismatch', result.stderr)
                if case == 'exhaust': self.assertIn('after 5 attempts', result.stderr)

    @unittest.skipUnless(os.environ.get('PORTICO_NETWORK_BUILD_TOOL_CONTROL') == '1', 'hosted exact upstream source control')
    def test_pinned_jellyfin_tls_shaderc_and_overlay_match_our_mac_flags_and_source(self):
        lock = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())
        extra = json.loads((ROOT / 'third_party/ffmpeg/macos-extras.lock.json').read_text())['openssl']
        def upstream(repository, commit, path):
            url = 'https://raw.githubusercontent.com/' + repository + '/' + commit + '/' + path
            with urllib.request.urlopen(url, timeout=30) as response:
                return response.read(1024**2).decode()
        jellyfin = upstream('jellyfin/jellyfin-ffmpeg', lock['ffmpeg']['commit'], 'builder/scripts.d/25-openssl.sh')
        btbn = upstream('BtbN/FFmpeg-Builds', lock['btbn']['commit'], 'scripts.d/25-openssl.sh')
        for script in (jellyfin, btbn):
            self.assertIn('SCRIPT_REPO="' + extra['repository'] + '"', script)
            self.assertIn('SCRIPT_COMMIT="' + extra['upstreamTag'] + '"', script)
        self.assertIn('echo --enable-openssl', jellyfin)
        self.assertIn('elif [[ $TARGET == mac* ]]', jellyfin)
        self.assertEqual(extra['version'], '3.6.3')
        self.assertEqual(extra['commit'], 'aae016bfd52fcad2bc9657c2c782cfdf73b1ed5f')
        overlay = upstream('jellyfin/jellyfin-ffmpeg', lock['ffmpeg']['commit'],
                           'debian/patches/0044-add-vf-overlay-videotoolbox-filter.patch')
        self.assertIn('overlay_videotoolbox_filter_deps="metal corevideo coreimage videotoolbox"', overlay)
        self.assertIn('ff_vf_overlay_videotoolbox;', overlay)
        self.assertIn('libavfilter/vf_overlay_videotoolbox.m', overlay)
        ours = (ROOT / 'scripts/build-macos-ffmpeg-extras.sh').read_text()
        self.assertIn('portico_clone openssl\ngit submodule update --init --recursive --depth=1', ours)
        for option in ('threads', 'zlib', 'no-shared', 'no-tests', 'no-apps', 'no-legacy', 'no-ssl3',
                       'enable-camellia', 'enable-ec', 'enable-srp'):
            self.assertIn(option, jellyfin)
            self.assertIn(option, ours)

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
            result = subprocess.run([sys.executable, ROOT / 'scripts/configure-ffmpeg-recipe.py', recipe, root, 'macos-arm64'],
                                    capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            configured = (recipe / 'build-ffmpeg').read_text()
            for option in ('--enable-coreimage', '--enable-libshaderc', '--enable-openssl'):
                self.assertIn('"' + option + '"', configured)
            self.assertNotIn('"--enable-securetransport"', configured)
            actual_download = configured[configured.index('download() {'):configured.index('execute() {')]
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
