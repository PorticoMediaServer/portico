"""Compile transport controls; these do not qualify an engine or its cache."""
import importlib.util
import os
from pathlib import Path
import tempfile
import unittest
import zipfile


def module(name, filename):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(filename))
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


cache = module('final_cache', 'engine-final-cache.py')
runner = module('runner', 'build-engine.py')


class FinalCacheTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(dir=Path(__file__).resolve().parents[1])
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.recipe = self.root / 'recipe'
        for name in ('util', 'variants', 'addins'):
            (self.recipe / name).mkdir(parents=True)
        (self.recipe / 'util/vars.sh').write_text('TARGET="$1"; VARIANT="$2"; ADDINS=("$3")\n')
        (self.recipe / 'variants/win64-gpl.sh').write_text('LICENSE_FILE=COPYING.GPLv3\n')
        (self.recipe / 'addins/8.1.sh').write_text('ADDIN_CONTROL=8.1\n')

    def test_same_shell_expansion_preserves_container_flags_and_target_selection(self):
        (self.recipe / 'build.sh').write_text('''cat <<EOF >"$BUILD_SCRIPT"
echo '$TARGET' '$VARIANT' '$ADDIN_CONTROL' "$SOURCE_DATE_EPOCH"
./configure \\$FFBUILD_TARGET_FLAGS \\$FF_CONFIGURE --cc="\\$CC"
EOF
''')
        output = self.recipe / 'portico-compile.sh'
        cache.compile_program(self.recipe, 'win64', output,
                              dict(os.environ, SOURCE_DATE_EPOCH='1234', CC='host-must-not-expand'))
        self.assertEqual(output.read_text(), "echo 'win64' 'gpl' '8.1' \"1234\"\n"
                         './configure $FFBUILD_TARGET_FLAGS $FF_CONFIGURE --cc="$CC"\n')
        (self.recipe / 'build.sh').write_text('changed upstream boundary')
        with self.assertRaisesRegex(ValueError, 'boundary changed'):
            cache.compile_program(self.recipe, 'win64', output, os.environ)

    def test_compile_is_descendant_of_every_dependency_and_invalidates_on_inputs(self):
        original = ('FROM compiler AS base-layer\nFROM base-layer AS lib\n'
                    'FROM base-layer AS combine-layer\nCOPY --from=lib /out /in\n'
                    'FROM base-layer\nCOPY --from=combine-layer /out /in\n'
                    'ENV FF_CONFIGURE="--enable-gpl --enable-version3"\n')
        changed = cache.stages(original, 1000, 1000, 1234)
        graph = runner.stages(changed)
        self.assertEqual(graph['portico-dependency-prefix'], ['base-layer', 'combine-layer'])
        self.assertEqual(graph['portico-ffmpeg'], ['portico-dependency-prefix'])
        self.assertEqual(graph['portico-engine-output'], ['portico-ffmpeg'])
        self.assertIn('USER 1000:1000', changed)
        self.assertIn('ENV SOURCE_DATE_EPOCH=1234', changed)
        self.assertEqual(changed.count('FF_CONFIGURE='), 1)
        (self.recipe / 'Dockerfile').write_text(changed)
        disc = self.recipe / 'portico-disc'
        disc.mkdir()
        (disc / 'source.c').write_text('helper source')
        for name in ('portico-ffmpeg-source.tar.xz', 'portico-compile.sh'):
            (self.recipe / name).write_text('first')
        results = {'portico-dependency-prefix': {'key': 'first-dependencies'}}
        def key():
            return runner.stage_key(self.recipe, 'portico-ffmpeg', graph['portico-ffmpeg'], 'compiler', results)
        before = key()
        (self.recipe / 'unrelated').write_text('packaging only')
        self.assertEqual(before, key())
        for path in (self.recipe / 'portico-compile.sh', self.recipe / 'portico-ffmpeg-source.tar.xz', disc / 'source.c'):
            content = path.read_text()
            path.write_text(content + ' changed')
            self.assertNotEqual(before, key())
            path.write_text(content)
        results['portico-dependency-prefix']['key'] = 'changed-library'
        self.assertNotEqual(before, key())
        for invalid in (original.replace('FROM base-layer\n', ''), original + 'FROM extra\n'):
            with self.assertRaises(ValueError):
                cache.stages(invalid, 1000, 1000, 1234)
        with self.assertRaises(ValueError):
            cache.stages(original, '1000; command', 1000, 1234)

    def test_gpl_licence_and_native_archive_paths_survive_packaging_and_links_fail(self):
        prefix = self.root / 'prefix'
        (prefix / 'bin').mkdir(parents=True)
        for name in ('ffmpeg.exe', 'ffprobe.exe', 'portico-disc.exe'):
            (prefix / 'bin' / name).write_bytes(b'non-executable transport fixture')
        (prefix / 'LICENSES').mkdir()
        (prefix / 'LICENSES/dependency.txt').write_text('complete licence fixture')
        variant = self.recipe / 'variants/win64-gpl.sh'
        variant.write_text(variant.read_text() + 'package_variant() { cp -R "$1"/. "$2"/; }\n')
        source = self.root / 'source'
        source.mkdir()
        (source / 'COPYING.GPLv3').write_text('GPL text fixture')
        # Windows packages its owned BuildKit prefix directly; BtbN's Actions
        # selector sits outside that input and cannot enter the output ZIP.
        (self.recipe / 'artifacts').mkdir()
        (self.recipe / 'artifacts/win64-gpl-8.1.txt').write_text('unused-btbn.zip\n')
        output = self.root / 'package'
        cache.package(self.recipe, 'win64', prefix, source, output, os.environ)
        archive = self.root / 'engine.zip'
        cache.archive(output, archive)
        with zipfile.ZipFile(archive) as zipped:
            self.assertEqual(zipped.read('package/LICENSE.txt'), b'GPL text fixture')
            self.assertIn('package/LICENSES/dependency.txt', zipped.namelist())
            self.assertFalse(any(name.endswith('.txt') and 'win64-gpl' in name for name in zipped.namelist()))
            self.assertTrue(all('\\' not in name for name in zipped.namelist()))
        (prefix / 'bin/link').symlink_to('ffmpeg.exe')
        with self.assertRaisesRegex(ValueError, 'links'):
            cache.package(self.recipe, 'win64', prefix, source, self.root / 'other', os.environ)
        (output / 'link').symlink_to('LICENSE.txt')
        with self.assertRaisesRegex(ValueError, 'links'):
            cache.archive(output, self.root / 'bad.zip')


    def repeat_fixture(self):
        source = self.root / 'source.tar.xz'; source.write_bytes(b'controlled source transport fixture')
        prefixes = [self.root / 'round-one', self.root / 'round-two']
        for prefix in prefixes:
            (prefix / 'bin').mkdir(parents=True)
            for name in ('ffmpeg', 'ffprobe', 'portico-disc'):
                (prefix / 'bin' / (name + '.exe')).write_bytes(b'non-executable repeat fixture: ' + name.encode())
        return source, *prefixes

    def test_two_independent_output_fingerprints_bind_all_tools_and_the_same_source(self):
        source, first, second = self.repeat_fixture()
        fingerprint = cache.round_fingerprint(first, source)
        records = cache.repeat_rounds(first, second, source, fingerprint)
        self.assertEqual([row['round'] for row in records], [1, 2])
        self.assertEqual(records[0]['binaries'], records[1]['binaries'])
        self.assertEqual(set(records[0]['binaries']), {'ffmpeg', 'ffprobe', 'portico-disc'})
        self.assertEqual(records[0]['sourceArchiveSHA256'], records[1]['sourceArchiveSHA256'])
        with self.assertRaisesRegex(ValueError, 'independent'):
            cache.repeat_rounds(first, first, source, fingerprint)

    def test_changed_source_or_first_output_and_each_mismatched_second_tool_refuse(self):
        source, first, second = self.repeat_fixture()
        fingerprint = cache.round_fingerprint(first, source)
        original = source.read_bytes(); source.write_bytes(b'changed source')
        with self.assertRaisesRegex(ValueError, 'first compile or source changed'):
            cache.repeat_rounds(first, second, source, fingerprint)
        source.write_bytes(original)
        for name in ('ffmpeg', 'ffprobe', 'portico-disc'):
            for prefix in (first, second):
                path = prefix / 'bin' / (name + '.exe'); original = path.read_bytes()
                path.write_bytes(b'changed executable transport fixture')
                with self.subTest(tool=name, prefix=prefix.name), self.assertRaises(ValueError):
                    cache.repeat_rounds(first, second, source, fingerprint)
                path.write_bytes(original)

    def test_missing_empty_or_linked_second_output_never_counts_as_a_matching_compile(self):
        source, first, second = self.repeat_fixture()
        fingerprint = cache.round_fingerprint(first, source)
        path = second / 'bin/portico-disc.exe'; path.unlink()
        with self.assertRaises(ValueError):
            cache.repeat_rounds(first, second, source, fingerprint)
        path.write_bytes(b'')
        with self.assertRaises(ValueError):
            cache.repeat_rounds(first, second, source, fingerprint)
        path.unlink(); path.symlink_to(first / 'bin/portico-disc.exe')
        with self.assertRaisesRegex(ValueError, 'links'):
            cache.repeat_rounds(first, second, source, fingerprint)


if __name__ == '__main__':
    unittest.main()
