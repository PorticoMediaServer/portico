#!/usr/bin/env python3
"""Offline source-custody controls; no engine acceptance or crate compilation."""
import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('configurator', Path(__file__).with_name('configure-windows-rav1e.py'))
configurator = importlib.util.module_from_spec(spec)
spec.loader.exec_module(configurator)


class Rav1eCustody(unittest.TestCase):
    def rav1e_fixture(self, root):
        recipe = root / 'recipe'
        (recipe / 'scripts.d').mkdir(parents=True)
        (root / 'third_party/ffmpeg').mkdir(parents=True)
        original = b'original locked source fixture\n'
        resolved = b'[[package]]\nname="cc"\nversion="1.10.0"\n'
        (root / 'third_party/ffmpeg/Cargo.lock').write_bytes(resolved)
        entry = {'repository': 'https://github.com/xiph/rav1e.git', 'commit': '1' * 40,
            'originalCargoLockSHA256': hashlib.sha256(original).hexdigest(),
            'resolvedCargoLock': {'path': 'third_party/ffmpeg/Cargo.lock',
                                 'sha256': hashlib.sha256(resolved).hexdigest()},
            'resolvedPackages': {'cc': '1.10.0'}}
        (root / 'third_party/ffmpeg/sources.lock.json').write_text(json.dumps({'btbn': {'rav1eCargo': entry}}))
        (recipe / 'download.sh').write_text('docker run -v "$PWD/portico-dovi-inputs":/portico-libdovi:ro image\n')
        (recipe / 'scripts.d/50-rav1e.sh').write_text('''SCRIPT_REPO="https://github.com/xiph/rav1e.git"
SCRIPT_COMMIT="1111111111111111111111111111111111111111"
ffbuild_dockerbuild() {
    local myconf=(--target=x86_64-pc-windows-gnu --library-type=staticlib --crt-static)
    # The pinned version is broken, and upstream does not react
    cargo update cc
    cargo cinstall -v "${myconf[@]}"
}
''')
        return recipe, original, resolved


    def test_rav1e_snapshot_keeps_both_locks_and_vendor_and_build_is_offline(self):
        with tempfile.TemporaryDirectory(dir=Path(__file__).resolve().parents[1]) as temp:
            root = Path(temp)
            recipe, original, resolved = self.rav1e_fixture(root)
            configurator.configure(recipe, root)
            script = recipe / 'scripts.d/50-rav1e.sh'
            subprocess.run(['bash', '-n', script], check=True)
            self.assertNotIn('cargo update', script.read_text())
            self.assertIn('portico-rav1e-inputs":/portico-rav1e:ro', (recipe / 'download.sh').read_text())
            program = subprocess.check_output(['bash', '-c',
                'default_dl() { :; }; source "$1"; ffbuild_dockerdl', 'control', str(script)], text=True)
            program = program.replace('/portico-rav1e/', str(recipe / 'portico-rav1e-inputs') + '/')
            work = root / 'work'
            work.mkdir()
            (work / 'Cargo.lock').write_bytes(original)
            fake_cargo = '''cargo() {
    if [[ "$1" == vendor ]]; then
        [[ "$*" == 'vendor --locked --versioned-dirs vendor' ]] || return 61
        mkdir -p "$CARGO_HOME" vendor/cc-1.10.0
        printf notice > vendor/cc-1.10.0/LICENSE
        printf '[source.crates-io]\\nreplace-with = "vendored-sources"\\n[source.vendored-sources]\\ndirectory = "vendor"\\n'
    elif [[ "$1" == cinstall ]]; then
        [[ "$*" == 'cinstall --locked --offline -v --target=x86_64-pc-windows-gnu --library-type=staticlib --crt-static' ]] || return 62
        [[ -f vendor/cc-1.10.0/LICENSE ]] || return 73
    else
        return 63
    fi
}
'''
            result = subprocess.run(['bash', '-ec', fake_cargo + program], cwd=work, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((work / 'portico-original-Cargo.lock').read_bytes(), original)
            self.assertEqual((work / 'Cargo.lock').read_bytes(), resolved)
            self.assertFalse((work / '.portico-cargo-home').exists())
            self.assertTrue((work / 'vendor/cc-1.10.0/LICENSE').is_file())
            subprocess.run(['bash', '-ec', fake_cargo + 'source "$1"; ffbuild_dockerbuild',
                            'control', str(script)], cwd=work, check=True, stdout=subprocess.DEVNULL)
            (work / 'Cargo.lock').write_bytes(b'tampered')
            refused = subprocess.run(['bash', '-ec', fake_cargo + 'source "$1"; ffbuild_dockerbuild',
                                      'control', str(script)], cwd=work, capture_output=True)
            self.assertNotEqual(refused.returncode, 0)
            (work / 'Cargo.lock').write_bytes(resolved)
            (work / 'vendor/cc-1.10.0/LICENSE').unlink()
            refused = subprocess.run(['bash', '-ec', fake_cargo + 'source "$1"; ffbuild_dockerbuild',
                                      'control', str(script)], cwd=work, capture_output=True)
            self.assertEqual(refused.returncode, 73)


    def test_rav1e_refuses_lock_hash_versions_and_source_drift(self):
        for change in ('hash', 'version', 'source'):
            with self.subTest(change=change), tempfile.TemporaryDirectory(dir=Path(__file__).resolve().parents[1]) as temp:
                root = Path(temp)
                recipe, _, _ = self.rav1e_fixture(root)
                if change == 'hash':
                    (root / 'third_party/ffmpeg/Cargo.lock').write_bytes(b'tampered')
                elif change == 'version':
                    path = root / 'third_party/ffmpeg/sources.lock.json'
                    lock = json.loads(path.read_text())
                    lock['btbn']['rav1eCargo']['resolvedPackages']['cc'] = '0.0.0'
                    path.write_text(json.dumps(lock))
                else:
                    path = recipe / 'scripts.d/50-rav1e.sh'
                    path.write_text(path.read_text().replace('1' * 40, '2' * 40))
                with self.assertRaises(ValueError):
                    configurator.configure(recipe, root)



if __name__ == '__main__':
    unittest.main()
