#!/usr/bin/env python3
"""Refusal controls; the native job separately exercises actual Rust linkers."""
import hashlib
import json
from pathlib import Path
import runpy
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
TOOLS = runpy.run_path(str(ROOT / 'scripts/ffmpeg-rust-tools.py'))
LINUX = runpy.run_path(str(ROOT / 'scripts/build-linux-libdovi.py'))


class NativeRustTools(unittest.TestCase):
    def test_pin_is_sealed_and_all_targets_use_the_shared_version(self):
        lock = json.loads((ROOT / 'third_party/ffmpeg/sources.lock.json').read_text())
        pin_file = ROOT / lock['nativeRustTools']['path']
        self.assertEqual(hashlib.sha256(pin_file.read_bytes()).hexdigest(), lock['nativeRustTools']['sha256'])
        pin = json.loads(pin_file.read_text())
        TOOLS['shared_versions'](pin, ROOT / 'third_party/media-engine/sources.lock.json')
        self.assertEqual(set(pin['rust']['targets']),
                         {'aarch64-apple-darwin', 'x86_64-apple-darwin', 'x86_64-unknown-linux-gnu'})
        for target, components in pin['rust']['targets'].items():
            self.assertEqual(set(components), {'rustc', 'cargo', 'rust-std'})
            for component, entry in components.items():
                self.assertIn(component + '-' + pin['rust']['version'] + '-' + target, entry['url'])
                self.assertRegex(entry['sha256'], r'^[0-9a-f]{64}$')

    def test_source_inventory_refuses_mutation_addition_and_links(self):
        with tempfile.TemporaryDirectory() as temporary:
            stage = Path(temporary)
            sources = stage / 'tool-sources'; sources.mkdir()
            cargo = sources / 'cargo-c.crate'; cargo.write_bytes(b'original source')
            expected = LINUX['tool_source_inventory'](stage)
            LINUX['verify_tool_sources'](stage, expected)
            cargo.write_bytes(b'changed source')
            with self.assertRaises(ValueError):
                LINUX['verify_tool_sources'](stage, expected)
            cargo.write_bytes(b'original source')
            extra = sources / 'unexpected'; extra.write_bytes(b'extra source')
            with self.assertRaises(ValueError):
                LINUX['verify_tool_sources'](stage, expected)
            extra.unlink(); extra.symlink_to(cargo)
            with self.assertRaises(ValueError):
                LINUX['tool_source_inventory'](stage)


if __name__ == '__main__':
    unittest.main()
