import importlib.util
from pathlib import Path
import tempfile
import unittest
import subprocess

spec = importlib.util.spec_from_file_location('engine', Path(__file__).with_name('build-engine.py'))
engine = importlib.util.module_from_spec(spec)
spec.loader.exec_module(engine)
spec = importlib.util.spec_from_file_location('adapter', Path(__file__).with_name('engine-recipe-adapter.py'))
adapter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(adapter)


class EngineTests(unittest.TestCase):
    def test_independent_failure_continues_and_blocks_descendants(self):
        visited = []
        def execute(name, deps):
            visited.append(name)
            if name == 'bad':
                raise RuntimeError('compiler failed')
            return {'state': 'passed'}
        result = engine.aggregate({'base': [], 'bad': ['base'], 'child': ['bad'],
                                   'independent': ['base'], 'combine': ['child', 'independent']}, execute)
        self.assertEqual(visited, ['base', 'bad', 'independent'])
        self.assertEqual(result['bad']['state'], 'failed')
        self.assertEqual(result['child']['prerequisites'], ['bad'])
        self.assertEqual(result['combine']['state'], 'blocked')

    def test_actual_graph_from_copy_and_from(self):
        graph = engine.stages('FROM compiler AS base-layer\nFROM base-layer AS a\n'
                              'FROM base-layer AS b\nCOPY --link --from=a /out /in\n')
        self.assertEqual(graph['b'], ['base-layer', 'a'])
        with self.assertRaises(ValueError):
            engine.stages('FROM compiler AS base-layer\nCOPY --from=unknown /a /b\n')

    def test_inventory_detects_corruption(self):
        with tempfile.TemporaryDirectory(dir=Path(__file__).resolve().parents[1]) as temp:
            root = Path(temp)
            (root / 'artifact').write_bytes(b'good')
            before = engine.inventory(root)
            (root / 'artifact').write_bytes(b'evil')
            self.assertNotEqual(before, engine.inventory(root))

    def test_group_dependencies_are_individually_addressable_without_losing_outputs(self):
        text = ('FROM compiler AS base-layer\nFROM base-layer AS fonts\n'
                'ENV SELF="scripts.d/fonts/10-first.sh"\n'
                'RUN --mount=src=scripts.d/fonts/10-first.sh,dst=/stage.sh run_stage /stage.sh\n'
                'ENV SELF="scripts.d/fonts/20-second.sh"\n'
                'RUN --mount=src=scripts.d/fonts/20-second.sh,dst=/stage.sh run_stage /stage.sh\n'
                'FROM base-layer AS combine-layer\nCOPY --from=fonts /out /in\n')
        changed = engine.dependency_steps(text)
        graph = engine.stages(changed)
        self.assertEqual(graph['fonts-10-first'], ['fonts-input'])
        self.assertEqual(graph['fonts-20-second'], ['fonts-10-first'])
        self.assertEqual(graph['fonts'], ['fonts-20-second'])
        self.assertEqual(graph['combine-layer'], ['base-layer', 'fonts'])
        for line in text.splitlines():
            if not line.startswith('FROM '):
                self.assertEqual(changed.count(line), 1)

    def test_buildkit_cache_classification_requires_all_source_build_steps(self):
        self.assertTrue(engine.cached_mounts('#1 [lib 1/1] RUN --mount=src=source,dst=/source build\n#1 CACHED\n'))
        self.assertFalse(engine.cached_mounts('#1 [lib 1/1] RUN --mount=src=source,dst=/source build\n#1 DONE 1s\n#2 CACHED\n'))
        self.assertTrue(engine.cached_mounts(b'warning: \xcc\n#1 [lib 1/1] RUN --mount=src=source,dst=/source build\n#1 CACHED\n'))
        self.assertFalse(engine.cached_mounts(b'warning: \xcc\n#1 [lib 1/1] RUN --mount=src=source,dst=/source build\n#1 DONE 1s\n'))

    def test_meson_diagnostics_keep_arguments_failure_status_and_output_bound(self):
        with tempfile.TemporaryDirectory(dir=Path(__file__).resolve().parents[1]) as temp:
            root = Path(temp)
            for command in ('meson "${myconf[@]}" ..', 'meson setup "${myconf[@]}" ..'):
                text = 'ffbuild_dockerbuild() {\n    local myconf=(--cross-file=/cross.meson --default-library=static)\n    ' + command + '\n    touch after-meson\n}\n'
                wrapped = adapter.meson_diagnostics(text, command, 'control')
                fake = '''meson() {
  printf 'arguments:%s\\n' "$*"
  mkdir -p meson-logs
  head -c 2097152 /dev/zero | tr '\\0' Q >meson-logs/meson-log.txt
  return 23
}
'''
                result = subprocess.run(['bash', '-c', fake + wrapped + 'ffbuild_dockerbuild'],
                                        cwd=root, capture_output=True)
                self.assertEqual(result.returncode, 23)
                self.assertFalse((root / 'after-meson').exists())
                self.assertIn(b'--cross-file=/cross.meson --default-library=static ..', result.stdout)
                self.assertIn(b'BEGIN control meson-log.txt', result.stdout)
                self.assertLess(len(result.stdout), 1048576 + 300)
                success = subprocess.run(['bash', '-c', 'meson() { return 0; }\n' + wrapped + 'ffbuild_dockerbuild'], cwd=root)
                self.assertEqual(success.returncode, 0)
                (root / 'after-meson').unlink()

    def test_windows_meson_initializes_private_wine_runtime_and_keeps_sanity(self):
        original = 'ffbuild_dockerbuild() {\n    meson setup --cross-file=/cross.meson ..\n}\n'
        changed = adapter.meson_wine_runtime(original)
        self.assertNotEqual(changed, original)
        fake = '''id() { printf '123456789\\n'; }
stat() { printf '123456789\\n'; }
install() { printf 'install:%s\\n' "$*"; return "${INSTALL_STATUS:-0}"; }
meson() { printf 'meson:%s;TMPDIR=%s\\n' "$*" "$TMPDIR"; return 23; }
'''
        for target in ('win64', 'winarm64', 'linux64'):
            result = subprocess.run(['bash', '-c', fake + changed +
                '\nTARGET=' + target + '; TMPDIR=/var/tmp/portico-ci; ffbuild_dockerbuild'],
                capture_output=True, text=True)
            self.assertEqual(result.returncode, 23)
            self.assertIn('meson:setup --cross-file=/cross.meson ..;TMPDIR=/var/tmp/portico-ci', result.stdout)
            self.assertEqual('install:' in result.stdout, target.startswith('win'))
            if target.startswith('win'):
                self.assertTrue(result.stdout.startswith('install:-d -m 0700 /run/user/123456789\n'))
        refused = subprocess.run(['bash', '-c', fake + changed +
            '\nTARGET=win64; INSTALL_STATUS=17; ffbuild_dockerbuild'], capture_output=True, text=True)
        self.assertEqual(refused.returncode, 17)
        self.assertNotIn('meson:', refused.stdout)
        self.assertEqual(adapter.meson_wine_runtime('ffbuild_dockerbuild() {\n    make\n}\n'),
                         'ffbuild_dockerbuild() {\n    make\n}\n')
        with self.assertRaisesRegex(ValueError, 'boundary changed'):
            adapter.meson_wine_runtime('    meson setup ..\n')

    def test_only_selected_sources_download_and_failures_do_not_cancel_independent_sources(self):
        with tempfile.TemporaryDirectory(dir=Path(__file__).resolve().parents[1]) as temp:
            root = Path(temp)
            source = root / '.cache/downloads'
            source.mkdir(parents=True)
            selected = ['good', 'bad', 'later']
            (root / 'Dockerfile').write_text(''.join('RUN --mount=src=scripts.d/' + name +
                '.sh,dst=/stage.sh --mount=src=.cache/downloads/' + name + '.tar.xz,dst=/cache.tar.xz build\n'
                for name in selected))
            for name in [*selected, 'unselected-fdk']:
                (root / (name + '.sh')).write_text('exit 7\n' if name == 'bad' else
                    'printf source > "' + str(source / (name + '.tar.xz')) + '"\n')
            command = adapter.selected_download("bash -c 'set -xe && for STAGE in /stages/*.sh; do bash $STAGE; done'")
            command = command.replace('/stages/', str(root) + '/')
            visited, updates = [], []
            def execute(script):
                visited.append(script)
                subprocess.run(['bash', '-c', command, 'control', Path(script).name],
                               check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            results, failed = engine.download_sources(root, execute, lambda r: updates.append(dict(r)))
            self.assertEqual(len(updates), 3)
            self.assertEqual(len(visited), 3)
            self.assertEqual(results['scripts.d/bad.sh']['state'], 'failed')
            self.assertEqual(results['scripts.d/later.sh']['state'], 'passed')
            self.assertEqual(failed, {'.cache/downloads/bad.tar.xz'})
            self.assertFalse((source / 'unselected-fdk.tar.xz').exists())

    def test_dependency_keys_change_only_with_their_inputs_and_prerequisites(self):
        with tempfile.TemporaryDirectory(dir=Path(__file__).resolve().parents[1]) as temp:
            root = Path(temp)
            (root / 'source').write_text('first')
            (root / 'unrelated').write_text('independent')
            (root / 'Dockerfile').write_text('FROM compiler AS base-layer\nFROM base-layer AS dep\n'
                'RUN --mount=src=source,dst=/source build\n')
            results = {'base-layer': {'key': 'parent'}}
            before = engine.stage_key(root, 'dep', ['base-layer'], 'sha256:' + 'a' * 64, results)
            (root / 'unrelated').write_text('changed')
            self.assertEqual(before, engine.stage_key(root, 'dep', ['base-layer'], 'sha256:' + 'a' * 64, results))
            (root / 'source').write_text('changed')
            self.assertNotEqual(before, engine.stage_key(root, 'dep', ['base-layer'], 'sha256:' + 'a' * 64, results))

    def test_cache_bound_preserves_noncache_evidence(self):
        with tempfile.TemporaryDirectory(dir=Path(__file__).resolve().parents[1]) as temp:
            root = Path(temp)
            (root / 'custody').mkdir()
            for name in ['stage-old', 'stage-new']:
                (root / name).mkdir()
                (root / name / 'data').write_bytes(b'12345678')
            engine.prune(root, 8)
            self.assertEqual(len(list(root.glob('stage-*'))), 1)
            self.assertTrue((root / 'custody').exists())


if __name__ == '__main__':
    unittest.main()
