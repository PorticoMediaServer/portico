#!/usr/bin/env python3
"""Actual pkg-config selection controls; not a native shaderc compilation proof."""
import hashlib
from pathlib import Path
import runpy
import tempfile
import unittest

M = runpy.run_path(str(Path(__file__).with_name('configure-macos-shaderc.py')))


class StaticShaderc(unittest.TestCase):
    def fixture(self, root):
        prefix = root / 'owned prefix'; directory = prefix / 'lib/pkgconfig'; directory.mkdir(parents=True)
        # Valid empty ar metadata fixture; no engine/library execution claim.
        (prefix / 'lib/libshaderc_combined.a').write_bytes(b'!<arch>\n')
        common = ('prefix=' + str(prefix) + '\nlibdir=${prefix}/lib\nName: shaderc metadata fixture\n'
                  'Description: Unit metadata only\nVersion: 1.0\nLibs: -L${libdir} ')
        (directory / 'shaderc.pc').write_text(common + '-lshaderc_shared\n')
        (directory / 'shaderc_combined.pc').write_text(common + '-lshaderc_combined\n')
        return prefix, directory

    def test_real_pkg_config_selects_static_module_and_preserves_the_original_combined_metadata(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); prefix, directory = self.fixture(root)
            before = (directory / 'shaderc_combined.pc').read_bytes()
            record = M['select_static'](prefix, root / 'receipt.json')
            self.assertIn('-lshaderc_combined', record['actualStaticLinkFlags'])
            self.assertIn('-lc++', record['actualStaticLinkFlags'])
            self.assertNotIn('-lshaderc_shared', record['actualStaticLinkFlags'])
            self.assertEqual((directory / 'shaderc_combined.pc').read_bytes(), before)
            self.assertEqual(record['combinedPCSHA256'], hashlib.sha256(before).hexdigest())
            self.assertFalse(record['runtimeAdded'])

    def test_shared_or_missing_archive_refuses_before_replacing_the_selected_module(self):
        for mutation in ('shared-pc', 'not-archive', 'symlink'):
            with self.subTest(mutation=mutation), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary); prefix, directory = self.fixture(root)
                if mutation == 'shared-pc':
                    (directory / 'shaderc_combined.pc').write_bytes((directory / 'shaderc.pc').read_bytes())
                elif mutation == 'not-archive':
                    (prefix / 'lib/libshaderc_combined.a').write_bytes(b'not an archive')
                else:
                    path = directory / 'shaderc_combined.pc'; path.unlink()
                    outside = root / 'outside.pc'; outside.write_text('outside')
                    path.symlink_to(outside)
                original = (directory / 'shaderc.pc').read_bytes()
                with self.assertRaises(ValueError):
                    M['select_static'](prefix, root / 'receipt.json')
                self.assertEqual((directory / 'shaderc.pc').read_bytes(), original)


if __name__ == '__main__':
    unittest.main()
