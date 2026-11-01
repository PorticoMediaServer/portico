#!/usr/bin/env python3
"""OCI cache transport and invalidation controls; no engine qualification."""
import importlib.util
import json
from pathlib import Path
import shutil
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('prefix', Path(__file__).with_name('engine-prefix-cache.py'))
M = importlib.util.module_from_spec(spec); spec.loader.exec_module(M)


class PrefixCache(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(); self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.roots = {name: self.root / name for name in ('prefix', 'sources')}
        for p in self.roots.values(): p.mkdir()
        self.cache = M.PrefixCache(self.root / 'store', self.root / 'state')
        self.inputs = dict(stage='library', compiler='sha256:' + 'a' * 64,
                           source='b' * 64, recipe='c' * 64, flags=['--static'])

    def reset(self):
        for p in self.roots.values(): shutil.rmtree(p); p.mkdir()

    def produce(self):
        self.assertFalse(self.cache.begin(self.inputs, self.roots)['hit'])
        library = self.roots['prefix'] / 'library.a'; library.write_bytes(b'inert dependency output'); library.chmod(0o444)
        (self.roots['prefix'] / 'library-link.a').symlink_to(library)
        (self.roots['sources'] / 'receipt.json').write_text('{"originalSourceSHA256":"fixture"}')
        return self.cache.finish(self.inputs, self.roots)

    def test_real_oci_layout_preserves_output_source_receipt_and_normalized_link(self):
        receipt = self.produce(); self.reset()
        hit = self.cache.begin(self.inputs, self.roots)
        self.assertTrue(hit['hit']); self.assertFalse(hit['receipt']['qualified']); self.assertFalse(hit['receipt']['release'])
        self.assertEqual((self.roots['prefix'] / 'library-link.a').read_bytes(), b'inert dependency output')
        self.assertEqual((self.roots['prefix'] / 'library-link.a').readlink(), Path('library.a'))
        self.assertTrue((self.roots['sources'] / 'receipt.json').is_file())
        layout = self.root / 'store/stages' / receipt['key']
        self.assertEqual(json.loads((layout / 'oci-layout').read_text())['imageLayoutVersion'], '1.0.0')

    def test_delta_can_link_to_an_unchanged_prior_stage_and_remove_a_file(self):
        self.produce()
        prior = M.inventory(self.roots)
        second = dict(self.inputs, stage='second')
        self.assertFalse(self.cache.begin(second, self.roots)['hit'])
        (self.roots['prefix'] / 'new-alias').symlink_to('library.a')
        (self.roots['prefix'] / 'library-link.a').unlink()
        self.cache.finish(second, self.roots)
        (self.roots['prefix'] / 'new-alias').unlink()
        (self.roots['prefix'] / 'library-link.a').symlink_to('library.a')
        self.assertEqual(M.inventory(self.roots), prior)
        self.assertTrue(self.cache.begin(second, self.roots)['hit'])
        self.assertEqual((self.roots['prefix'] / 'new-alias').read_bytes(), b'inert dependency output')
        self.assertFalse((self.roots['prefix'] / 'library-link.a').exists())

    def test_each_exact_input_or_prior_output_change_is_a_miss(self):
        self.produce(); self.reset()
        for field in ('source', 'compiler', 'recipe', 'flags'):
            self.assertFalse(self.cache.begin(dict(self.inputs, **{field: 'changed'}), self.roots)['hit'])
        (self.roots['prefix'] / 'unexpected').write_bytes(b'parent changed')
        self.assertFalse(self.cache.begin(self.inputs, self.roots)['hit'])

    def test_corrupt_blob_refuses_before_output_restoration(self):
        receipt = self.produce(); self.reset()
        layout = self.root / 'store/stages' / receipt['key']
        index = json.loads((layout / 'index.json').read_text())
        manifest = M.checked_blob(layout, index['manifests'][0]); manifest.write_bytes(b'corrupt blob')
        with self.assertRaisesRegex(ValueError, 'blob changed'): self.cache.begin(self.inputs, self.roots)
        self.assertEqual(list(self.roots['prefix'].iterdir()), [])

    def test_escape_special_and_failed_stage_never_create_a_cache_entry(self):
        outside = self.root / 'outside'; outside.write_bytes(b'outside')
        (self.roots['prefix'] / 'link').symlink_to(outside)
        with self.assertRaisesRegex(ValueError, 'escapes'): self.cache.begin(self.inputs, self.roots)
        (self.roots['prefix'] / 'link').unlink()
        self.cache.begin(self.inputs, self.roots)
        self.assertEqual(list((self.root / 'store/stages').iterdir()), [])


if __name__ == '__main__': unittest.main()
