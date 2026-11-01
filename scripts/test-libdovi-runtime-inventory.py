#!/usr/bin/env python3
"""Exercise actual notice copying before selecting the target's Rust archives."""
import hashlib
import json
from pathlib import Path
import runpy
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
M = runpy.run_path(str(ROOT / 'scripts/build-linux-libdovi.py'))


class RuntimeInventory(unittest.TestCase):
    def test_linux_windows_and_macos_keep_triples_after_notice_copying(self):
        for triple in ('x86_64-unknown-linux-gnu', 'x86_64-pc-windows-gnu', 'aarch64-unknown-linux-gnu', 'aarch64-apple-darwin', 'x86_64-apple-darwin'):
            with self.subTest(triple=triple), tempfile.TemporaryDirectory(dir=ROOT) as tmp:
                root = Path(tmp)
                stage, prefix, sysroot = root / 'stage', root / 'prefix', root / 'sysroot'
                (stage / 'source/dolby_vision').mkdir(parents=True)
                (stage / 'vendor').mkdir()
                (stage / 'license-supplements').mkdir()
                (stage / 'contract.json').write_bytes((ROOT / 'third_party/ffmpeg/portico-dovi-profile.v1.json').read_bytes())
                supplements = stage / 'license-supplements/manifest.json'
                supplements.write_text('{"modules": {}}')
                (stage / 'expected.json').write_text(json.dumps({'licenseSupplementsSHA256':
                    hashlib.sha256(supplements.read_bytes()).hexdigest()}))
                (stage / 'source/dolby_vision/Cargo.toml').write_text(
                    '[package]\nname="dolby_vision"\nversion="3.4.0"\nlicense="MIT"\n')
                (stage / 'source/dolby_vision/LICENSE').write_text('complete test crate licence')
                docs = sysroot / 'share/doc/rust'
                (docs / 'licenses').mkdir(parents=True)
                for name in ('COPYRIGHT-library.html', 'COPYRIGHT.html', 'licenses/MIT.txt'):
                    (docs / name).write_text('test Rust notice: ' + name)
                runtime = sysroot / 'lib/rustlib' / triple / 'lib'
                runtime.mkdir(parents=True)
                standard = runtime / 'libstd-test.rlib'
                standard.write_bytes(triple.encode())

                def output(command, **kwargs):
                    if command[:2] == ['pkg-config', '--modversion']:
                        return '3.4.0\n'
                    if command == ['rustc', '--print', 'sysroot']:
                        return str(sysroot) + '\n'
                    return 'test compiler version\n'

                with patch('subprocess.check_output', side_effect=output):
                    if triple == 'x86_64-unknown-linux-gnu':
                        M['inside'](stage, prefix)  # Exercise the unchanged Linux default.
                    else:
                        M['inside'](stage, prefix, triple)
                receipt = json.loads((stage / 'compiler-and-licenses.json').read_text())
                self.assertEqual(receipt['rustStandardLibraryArchiveInventory'],
                    {'libstd-test.rlib': hashlib.sha256(triple.encode()).hexdigest()})
                self.assertGreater(len(receipt['rustRuntimeNotices']), 0)
                self.assertEqual(len(receipt['rustRuntimeNotices']), 3)
                for name, expected in receipt['rustRuntimeNotices'].items():
                    self.assertEqual(hashlib.sha256((prefix / name).read_bytes()).hexdigest(), expected)


if __name__ == '__main__':
    unittest.main()
