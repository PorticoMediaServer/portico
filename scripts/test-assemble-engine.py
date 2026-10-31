#!/usr/bin/env python3
"""Bounded source/licence and raw-artifact checks, without compiling an engine."""
import io
from pathlib import Path
import runpy
import struct
import tarfile
import tempfile
import unittest
import zipfile

ROOT = Path(__file__).resolve().parents[1]
M = runpy.run_path(str(ROOT / 'scripts/assemble-engine.py'))


class AssemblyTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(dir=ROOT)
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)

    def test_collects_referenced_font_licences_and_patents(self):
        path = self.root / 'source.tar.xz'
        texts = {'LICENSE': b'see docs/FTL.TXT', 'docs/FTL.TXT': b'full font licence',
                 'docs/GPLv2.TXT': b'full GPL', 'PATENTS': b'patent grant', 'source.c': b'code'}
        with tarfile.open(path, 'w:xz') as archive:
            for name, data in texts.items():
                entry = tarfile.TarInfo(name)
                entry.size = len(data)
                archive.addfile(entry, io.BytesIO(data))
        notices = M['source_licenses'](path, self.root / 'licenses')
        self.assertEqual(set(notices), set(texts) - {'source.c'})
        for name in notices:
            self.assertEqual((self.root / 'licenses' / name).read_bytes(), texts[name])

    def test_missing_license_and_source_escape_fail(self):
        for name in ('source.c', '../LICENSE'):
            path = self.root / 'source.tar.xz'
            with tarfile.open(path, 'w:xz') as archive:
                entry = tarfile.TarInfo(name)
                entry.size = 4
                archive.addfile(entry, io.BytesIO(b'text'))
            with self.assertRaises(ValueError):
                M['source_licenses'](path, self.root / 'licenses')

    def test_source_pairs_require_the_exact_generated_snapshots(self):
        (self.root / 'Dockerfile').write_text('RUN --mount=src=scripts.d/lib.sh,dst=/stage.sh '
            '--mount=src=.cache/downloads/lib_hash.tar.xz,dst=/cache.tar.xz build\n')
        with self.assertRaises(ValueError):
            M['source_pairs'](self.root)
        (self.root / '.cache/downloads').mkdir(parents=True)
        (self.root / '.cache/downloads/lib_hash.tar.xz').write_bytes(b'source snapshot')
        self.assertEqual(M['source_pairs'](self.root), {'scripts.d/lib.sh': '.cache/downloads/lib_hash.tar.xz'})

    def test_raw_compilation_receipt_binds_both_archive_and_recipe(self):
        raw = self.root / 'raw.zip'
        raw.write_bytes(b'transport test input')
        recipe = self.root / 'recipe'
        recipe.mkdir()
        (recipe / 'build.sh').write_text('pinned recipe')
        (recipe / 'Dockerfile').write_text('consumed graph')
        report = {'rawCompilation': True, 'qualified': False, 'target': 'windows-x64',
            'engineSourceSHA': '1' * 40, 'sourceAuthoritySHA256': '2' * 64,
            'dockerfileSHA256': M['digest'](recipe / 'Dockerfile'),
            'recipeTreeSHA256': M['inputs'].json_digest(M['inputs'].recipe_tree(recipe)),
            'configureScripts': M['inputs'].configure_scripts(ROOT),
            'rawArchives': {raw.name: M['digest'](raw)}, 'recipeFiles': M['recipe_inventory'](recipe)}
        M['build_proof'](report, 'windows-x64', raw, recipe, '1' * 40, '2' * 64)
        (recipe / 'build.sh').write_text('changed source input')
        with self.assertRaises(ValueError):
            M['build_proof'](report, 'windows-x64', raw, recipe, '1' * 40, '2' * 64)
        with self.assertRaises(ValueError):
            M['build_proof'](report, 'windows-x64', raw, recipe, '3' * 40, '2' * 64)

    def test_sealed_receipt_uses_the_exact_consumed_graph_and_matches_archive_inventory(self):
        recipe = self.root / 'recipe'
        (recipe / 'scripts.d').mkdir(parents=True)
        (recipe / '.cache/downloads').mkdir(parents=True)
        snapshot = '.cache/downloads/10-dep_' + 'a' * 64 + '.tar.xz'
        (recipe / snapshot).write_bytes(b'source transport fixture')
        (recipe / '.cache/downloads/10-dep.tar.xz').symlink_to(Path(snapshot).name)
        (recipe / 'scripts.d/10-dep.sh').write_text('SCRIPT_REPO=transport-fixture\n')
        for name in ('build.sh', 'makeimage.sh'):
            (recipe / name).write_text('transport fixture')
        dockerfile = recipe / 'Dockerfile'
        original = 'FROM compiler AS base-layer\nRUN --mount=src=scripts.d/10-dep.sh,dst=/stage.sh --mount=src=' + snapshot + ',dst=/cache.tar.xz build\n'
        dockerfile.write_text(original)
        consumed_sha = M['digest'](dockerfile)
        self.assertEqual(M['inputs'].consumed_dockerfile(recipe, consumed_sha), dockerfile)
        dockerfile.write_text('regenerated graph is not the build input')
        with self.assertRaisesRegex(ValueError, 'Dockerfile changed'):
            M['inputs'].consumed_dockerfile(recipe, consumed_sha)
        dockerfile.write_text(original)
        report = {'target': 'windows-x64', 'engineSourceSHA': '1' * 40,
            'dockerfileSHA256': consumed_sha, 'recipeTreeSHA256': M['inputs'].json_digest(M['inputs'].recipe_tree(recipe)),
            'configureScripts': M['inputs'].configure_scripts(ROOT)}
        archive_path = self.root / 'source.tar.xz'
        with tarfile.open(archive_path, 'w:xz') as archive:
            archive.add(recipe, arcname='recipe')
            for name in ('provenance/contract/sources.lock.json', 'provenance/contract/build-source-authority.json',
                         'provenance/scripts/build-ffmpeg.sh', 'provenance/portico-disc/LICENSE',
                         'provenance/portico-disc/portico-disc.c', 'provenance/contract/patches/example.patch'):
                data = b'transport fixture'
                entry = tarfile.TarInfo(name)
                entry.size = len(data)
                archive.addfile(entry, io.BytesIO(data))
            for name in report['configureScripts']:
                archive.add(ROOT / 'scripts' / Path(name).name, arcname=name)
        receipt = M['source_build_receipt'](report, archive_path)
        self.assertEqual(receipt['dockerfileSHA256'], consumed_sha)
        M['intake'].verify_source(archive_path, 'windows-x64', receipt)

    def test_corresponding_source_inventory_rejects_broken_or_external_links(self):
        recipe = self.root / 'recipe'
        recipe.mkdir()
        archive = recipe / 'source.tar.xz'
        archive.write_bytes(b'checked source')
        link = recipe / 'alias.tar.xz'
        link.symlink_to(archive.name)
        self.assertEqual(M['recipe_inventory'](recipe)['alias.tar.xz'], M['digest'](archive))
        archive.unlink()
        with self.assertRaises(ValueError):
            M['recipe_inventory'](recipe)
        link.unlink()
        outside = self.root / 'outside'
        outside.write_bytes(b'outside source')
        link.symlink_to(outside)
        with self.assertRaises(ValueError):
            M['recipe_inventory'](recipe)

    def test_raw_backslash_null_traversal_and_symlink_rejected_before_extraction(self):
        path = self.root / 'raw.zip'
        for name in ('../escape', 'folder\\escape', 'folder\x00escape'):
            with zipfile.ZipFile(path, 'w') as archive:
                entry = zipfile.ZipInfo(name)
                entry.filename = name
                archive.writestr(entry, b'data')
            with self.assertRaises(ValueError):
                M['extract_raw'](path, self.root / 'out')
            self.assertFalse((self.root / 'out').exists())

    def test_pe_target_identity_and_gpl_fdk_policy(self):
        path = self.root / 'ffmpeg.exe'
        data = bytearray(128)
        data[:2] = b'MZ'
        struct.pack_into('<I', data, 60, 80)
        data[80:84] = b'PE\0\0'
        struct.pack_into('<H', data, 84, 0x8664)
        path.write_bytes(data)
        M['pe'](path, 'windows-x64')
        with self.assertRaises(ValueError):
            M['pe'](path, 'windows-arm64')
        for flags in ('--enable-gpl --enable-version3 --disable-libfdk-aac',
                      '--enable-gpl --enable-version3 --enable-libfdk-aac'):
            path.write_bytes(b'\x00' + flags.encode() + b'\x00')
            if '--enable-libfdk' in flags:
                with self.assertRaises(ValueError):
                    M['configuration'](path)
            else:
                self.assertEqual(M['configuration'](path), [flags])


if __name__ == '__main__':
    unittest.main()
