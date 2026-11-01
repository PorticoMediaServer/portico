#!/usr/bin/env python3
"""The release version and build number (scripts/release-version.py)."""
import json
import pathlib
import runpy
import subprocess
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parent.parent
RV = runpy.run_path(str(ROOT / 'scripts/release-version.py'))


class Version(unittest.TestCase):
    def test_only_zero_zero_n_until_v010(self):
        for good in ('0.0.0', '0.0.1', '0.0.12'):
            self.assertEqual(RV['check_version'](good, []), good)
        for bad in ('0.1.0', '1.0.0', '0.0.01', 'v0.0.1', '0.0.1-beta.1', '0.0', ''):
            with self.assertRaises(SystemExit, msg=bad):
                RV['check_version'](bad, [])

    def test_higher_than_every_existing_release_tag(self):
        existing = ['v0.0.1', 'v0.0.3-rc.1', 'apikit/v0.1.1', 'ffmpeg-8.1.3-jellyfin-portico.3', 'sonic-1.20.1-mtt-1']
        with self.assertRaises(SystemExit):
            RV['check_version']('0.0.3', existing)  # v0.0.3-rc.1 already names 0.0.3
        with self.assertRaises(SystemExit):
            RV['check_version']('0.0.1', existing)
        self.assertEqual(RV['check_version']('0.0.4', existing), '0.0.4')
        # Other products' and toolchains' tags never count as Portico releases.
        self.assertEqual(RV['check_version']('0.0.1', ['apikit/v0.1.1', 'ffmpeg-8.1.3-jellyfin-portico.3']), '0.0.1')


class BuildNumber(unittest.TestCase):
    def repo(self):
        d = pathlib.Path(self.enterContext(tempfile.TemporaryDirectory()))
        run = lambda *a: subprocess.run(['git', '-C', str(d), *a], check=True, capture_output=True, text=True).stdout.strip()
        run('init', '-q', '-b', 'main')
        run('config', 'user.email', 't@example.invalid'); run('config', 'user.name', 'T')
        for i in range(3):
            run('commit', '-q', '--allow-empty', '-m', f'c{i}')
        run('update-ref', 'refs/remotes/origin/main', 'HEAD')
        main = run('rev-parse', 'HEAD')
        run('checkout', '-q', '-b', 'side', 'HEAD~1'); run('commit', '-q', '--allow-empty', '-m', 'side')
        side = run('rev-parse', 'HEAD')
        return d, main, side

    def test_position_on_main_and_refusal_off_main(self):
        d, main, side = self.repo()
        number = runpy.run_path(str(ROOT / 'scripts/release-version.py'), init_globals={})
        # Point the helper at the throwaway repository.
        build_number = number['build_number']
        build_number.__globals__['ROOT'] = d
        self.assertEqual(build_number(main), 3)
        self.assertEqual(build_number(main + '~1'), 2, 'the same commit always gets the same number')
        with self.assertRaises(SystemExit):
            build_number(side)
        self.assertEqual(build_number(side, proof=True), 3, 'a proof run may build a branch')


class Stamp(unittest.TestCase):
    def tree(self):
        d = pathlib.Path(self.enterContext(tempfile.TemporaryDirectory()))
        for rel in ('apps/vega/manifest.toml', 'apps/desktop/src-tauri/tauri.conf.json'):
            (d / rel).parent.mkdir(parents=True, exist_ok=True)
            (d / rel).write_text((ROOT / rel).read_text())
        return d

    def test_vega_package_version_only(self):
        d = self.tree()
        RV['stamp']('vega', '0.0.7', 207, root=d)
        text = (d / 'apps/vega/manifest.toml').read_text()
        self.assertIn('[package]\ntitle = "Portico"\nversion = "0.0.7"', text)
        original = (ROOT / 'apps/vega/manifest.toml').read_text()
        self.assertEqual(text.count('version'), original.count('version'), 'no other version line moved')
        self.assertIn('[os.version]', text)

    def test_desktop_version(self):
        d = self.tree()
        RV['stamp']('desktop', '0.0.7', 207, root=d)
        conf = json.loads((d / 'apps/desktop/src-tauri/tauri.conf.json').read_text())
        self.assertEqual(conf['version'], '0.0.7')
        before = json.loads((ROOT / 'apps/desktop/src-tauri/tauri.conf.json').read_text())
        before['version'] = '0.0.7'
        self.assertEqual(conf, before, 'nothing else changed')


if __name__ == '__main__':
    unittest.main()
