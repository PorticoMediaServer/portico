import contextlib
import io
import json
import os
import re
from pathlib import Path
import subprocess
import tempfile
import sys
import unittest
from unittest.mock import patch

import plan
import run


class FullPackageSelection(unittest.TestCase):
    def setUp(self):
        self.packages = [
            {'ImportPath': 'a', 'Dir': str(plan.ROOT / 'server/a')},
            {'ImportPath': 'b', 'Dir': str(plan.ROOT / 'server/b'), 'Imports': ['a']},
            {'ImportPath': 'c', 'Dir': str(plan.ROOT / 'server/c'), 'TestImports': ['a']},
            {'ImportPath': 'x', 'Dir': str(plan.ROOT / 'server/x'), 'XTestImports': ['a']},
            {'ImportPath': 'transitive', 'Dir': str(plan.ROOT / 'server/transitive'), 'Imports': ['b']},
            {'ImportPath': 'other', 'Dir': str(plan.ROOT / 'apikit/other')},
        ]

    def test_changed_files_and_testdata_select_owner_and_one_reverse_hop(self):
        for path in ['server/a/file.go', 'server/a/file_test.go', 'server/a/testdata/input.json']:
            with self.subTest(path=path):
                self.assertEqual(plan.full_go_packages(self.packages, [path]), {'a', 'b', 'c', 'x'})

    def test_nested_package_is_not_attributed_to_its_parent(self):
        nested = {'ImportPath': 'nested', 'Dir': str(plan.ROOT / 'server/a/nested')}
        self.assertEqual(plan.full_go_packages(self.packages + [nested], ['server/a/nested/file.go']), {'nested'})

    def test_go_module_or_workspace_change_runs_every_package_full(self):
        for path in ['server/go.mod', 'apikit/go.mod', 'server/go.sum', 'go.work', 'go.work.sum']:
            with self.subTest(path=path):
                self.assertEqual(plan.full_go_packages(self.packages, [path]), {p['ImportPath'] for p in self.packages})

    def test_docs_and_non_go_changes_do_not_promote_packages(self):
        for paths in [[], ['README.md', 'server/a/README.md'], ['web/src/a.ts'], ['ci/plan.py']]:
            self.assertEqual(plan.full_go_packages(self.packages, paths), set())

    def test_deleted_embeds_keep_owner_from_directory_and_test_patterns(self):
        for field, pattern, path in [
                ('EmbedPatterns', 'assets', 'assets/removed.md'),
                ('TestEmbedPatterns', 'fixtures/*.svg', 'fixtures/removed.svg'),
                ('XTestEmbedPatterns', 'all:images', 'images/removed.png')]:
            with self.subTest(field=field):
                packages = [dict(p) for p in self.packages]
                packages[0][field] = [pattern]
                self.assertEqual(plan.full_go_packages(packages, ['server/a/' + path]), {'a', 'b', 'c', 'x'})
                self.assertEqual(plan.full_go_packages(packages, ['server/a/README.md']), set())

    def test_promoted_prs_can_use_twenty_shards_without_raising_the_feedback_cap(self):
        tests = [{'package': 'p', 'name': 'Test' + str(i)} for i in range(200)]
        costs = {'p.' + t['name']: 20 for t in tests}
        self.assertEqual(len(plan.balance_short_censuses(tests, costs, max_count=20)[0]), 20)
        self.assertEqual(len(plan.balance_short_censuses(tests, costs)[0]), 10)

    def test_batch_crash_parents_keep_complete_inventory_on_separate_shards(self):
        package = 'github.com/PorticoMediaServer/portico/server/internal/store/writer'
        parents = {'TestCrashReplayInventoryBatchEveryStepWidth2',
                   'TestCrashReplayInventoryBatchEveryStepWidth8A',
                   'TestCrashReplayInventoryBatchEveryStepWidth8B'}
        source = (plan.ROOT / 'server/internal/store/writer/crash_scan_batch_linux_test.go').read_text()
        self.assertEqual(set(re.findall(r'^func (TestCrashReplayInventoryBatchEveryStep\w*)\(', source, re.M)), parents)
        durations = json.loads((plan.ROOT / 'ci/durations.json').read_text())
        for name in parents:
            self.assertGreater(durations[package + '.' + name], 150)
        tests = [{'package': package, 'name': name} for name in sorted(parents)]
        tests += [{'package': package, 'name': 'TestFast' + str(i)} for i in range(1000)]
        for limit in (10, 20):
            with self.subTest(shard_limit=limit):
                bins, costs = plan.balance(tests, durations, max_count=limit)
                owners = [i for i, shard in enumerate(bins) for test in shard if test['name'] in parents]
                self.assertEqual(len(owners), 3)
                self.assertEqual(len(set(owners)), 3)
                self.assertEqual(sorted(t['name'] for shard in bins for t in shard), sorted(t['name'] for t in tests))
                self.assertLess(max(costs), 780)


class FullPlan(unittest.TestCase):
    def test_fixture_and_embed_suffixes_precede_documentation_filtering(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            packages = self.fixture(root)
            packages[0].update(EmbedFiles=['embedded.md'], TestEmbedFiles=['embedded.svg'],
                               XTestEmbedFiles=['embedded.png'])
            security = root / 'server/internal/httpapi'
            security.mkdir(parents=True)
            (security / 'a_test.go').write_text('package sample\nfunc TestSecurity(t *testing.T) {}\n')
            packages.append({'ImportPath': 'example/internal/httpapi', 'Dir': str(security), 'TestGoFiles': ['a_test.go']})
            inputs = ['server/a/testdata/titlepage.svg', 'server/a/testdata/cover.png',
                      'server/a/testdata/notes.md', 'server/a/embedded.md',
                      'server/a/embedded.svg', 'server/a/embedded.png']
            for name in inputs + ['server/a/README.md', 'server/README.md']:
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text('fixture or documentation')
            def command(args, cwd=None):
                if args[0] == 'python3': return 'tags'
                return '\n'.join(map(json.dumps, packages)) if Path(cwd).name == 'server' else ''
            with patch('plan.ROOT', root), patch('plan.command', side_effect=command), \
                 patch('plan.source_readers.select', return_value={}):
                for path in inputs:
                    for tier in ['short', 'merge']:
                        with self.subTest(path=path, tier=tier):
                            selection = plan.make_plan([path], tier=tier)
                            self.assertIn('a', {p['package'] for p in selection['packages']})
                            self.assertFalse(run.short_package(selection, 'a'))
                for tier in ['short', 'merge']:
                    selection = plan.make_plan(['server/a/README.md', 'server/README.md'], tier=tier)
                    self.assertEqual(selection['packages'], [])
                    self.assertEqual(selection['full_go_packages'], [])

    def test_cli_cross_package_rename_promotes_both_owners(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            self.fixture(root)
            packages = []
            for name in ['persistence', 'httpapi']:
                directory = root / 'server/internal' / name
                directory.mkdir(parents=True)
                (directory / 'a_test.go').write_text('package sample\nfunc TestOwner(t *testing.T) {}\n')
                packages.append({'ImportPath': name, 'Dir': str(directory), 'TestGoFiles': ['a_test.go']})
            def git(*args):
                return subprocess.check_output(['git', '-c', 'user.name=Justin Ehler', '-c',
                    'user.email=justin@justinehler.com', *args], cwd=root, text=True, stderr=subprocess.DEVNULL).strip()
            git('init', '-q')
            git('config', 'diff.renames', 'true')
            source = 'server/internal/persistence/testdata/derived-writers.json'
            destination = 'server/internal/httpapi/testdata/derived-writers.json'
            for path in [source, destination]:
                (root / path).parent.mkdir(parents=True, exist_ok=True)
            (root / source).write_text('{"fixture": "unchanged content"}\n')
            git('add', '.')
            git('commit', '-qm', 'Add package fixtures')
            base = git('rev-parse', 'HEAD')
            (root / source).rename(root / destination)
            git('add', '-A')
            git('commit', '-qm', 'Move unchanged fixture between packages')
            head = git('rev-parse', 'HEAD')
            self.assertTrue(git('diff', '--name-status', base, head).startswith('R100\t'))
            self.assertEqual(git('diff', '--name-only', base, head), destination)  # the inherited trap
            event = root / 'event.json'
            event.write_text(json.dumps({'before': base}))
            output = root / 'selection.json'
            actual_command = plan.command
            def command(args, cwd=None):
                if args[0] == 'python3': return 'tags'
                if args[0] == 'go':
                    return '\n'.join(map(json.dumps, packages)) if Path(cwd).name == 'server' else ''
                return actual_command(args, cwd=root)
            with patch('plan.ROOT', root), patch('plan.command', side_effect=command), \
                 patch('plan.source_readers.select', return_value={}), \
                 patch.dict(os.environ, {'GITHUB_EVENT_NAME': 'push', 'GITHUB_EVENT_PATH': str(event)}, clear=True), \
                 patch.object(sys, 'argv', ['plan.py', '--output', str(output)]):
                plan.main()
            selection = json.loads(output.read_text())
            self.assertEqual(set(selection['paths']), {source, destination})
            self.assertEqual(set(selection['full_go_packages']), {'persistence', 'httpapi'})
            self.assertEqual({t['package'] for t in selection['inventory']}, {'persistence', 'httpapi'})
            for package in ['persistence', 'httpapi']:
                self.assertFalse(run.short_package(selection, package))

    def test_request_cost_parent_will_run_without_short_on_a_route_change(self):
        package = 'github.com/PorticoMediaServer/portico/server/internal/httpapi'
        def command(args, cwd=None):
            if args[0] == 'python3': return 'tags'
            return json.dumps({'ImportPath': package, 'Dir': str(plan.ROOT / 'server/internal/httpapi'),
                               'TestGoFiles': ['request_cost_test.go']}) if Path(cwd).name == 'server' else ''
        with patch('plan.command', side_effect=command), patch('plan.source_readers.select', return_value={}):
            selection = plan.make_plan(['server/internal/httpapi/request_cost_test.go'])
        self.assertIn('TestRequestCostIsAttributedToOneRoute', [t['name'] for t in selection['inventory']])
        self.assertFalse(run.short_package(selection, package))

    def fixture(self, root):
        (root / 'ci').mkdir()
        (root / 'README.md').write_text('docs')
        (root / 'ci/durations.json').write_text(json.dumps({p + '.TestSlow': 60 for p in ['a', 'b', 'c', 'reader']}))
        (root / 'ci/tiers.json').write_text('{"nightly_only": [], "nightly_lanes": {}}')
        (root / 'ci/js-durations.json').write_text('{"seconds": {}}')
        packages = []
        for name, imports in [('a', []), ('b', ['a']), ('c', ['b']), ('reader', [])]:
            directory = root / 'server' / name
            directory.mkdir(parents=True)
            (directory / 'a_test.go').write_text('''package example
func TestMain(m *testing.M) {}
func TestFast(t *testing.T) {}
func TestSlow(t *testing.T) {
    if testing.Short() { t.Skip("full only") }
}
func Testlower(t *testing.T) {}
func Test_Upper(t *testing.T) {}
func FuzzSeeds(f *testing.F) {}
func ExamplePrinted() {
    // Output: hello
}
func ExampleCompileOnly() {}
''')
            packages.append({'ImportPath': name, 'Dir': str(directory), 'Imports': imports,
                             'TestGoFiles': ['a_test.go']})
        return packages

    def test_full_mode_costs_inventory_regex_and_source_readers(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            packages = self.fixture(root)
            def command(args, cwd=None):
                if args[0] == 'python3': return 'tags'
                return '\n'.join(map(json.dumps, packages)) if Path(cwd).name == 'server' else ''
            readers = {'server/reader': {'TestSlow': ['server/a/file.go']}}
            with patch('plan.ROOT', root), patch('plan.command', side_effect=command), \
                 patch('plan.source_readers.select', return_value=readers):
                result = plan.make_plan(['server/a/file.go'])
                self.assertEqual(result['full_go_packages'], ['a', 'b'])
                self.assertEqual({p['package'] for p in result['packages']}, {'a', 'b', 'c', 'reader'})
                self.assertEqual(result['full_go_extra_seconds'], 120)
                self.assertEqual([t['name'] for t in result['inventory'] if t['package'] == 'reader'], ['TestSlow'])
                self.assertEqual(result['source_reader_tests'][0]['changed'], ['server/a/file.go'])
                self.assertEqual({t['name'] for t in result['all_inventory'] if t['package'] == 'a'},
                                 {'TestFast', 'TestSlow', 'Test_Upper', 'FuzzSeeds', 'ExamplePrinted'})
                # Merge/nightly never invoke PR promotion and keep the same full cost.
                with patch('plan.full_go_packages', side_effect=AssertionError('PR policy used in queue')):
                    merge = plan.make_plan(['server/a/file.go'], tier='merge')
                    with patch('plan.components', return_value=([], True)):
                        nightly = plan.make_plan([], full=True, tier='nightly')
                self.assertEqual(merge['full_go_packages'], [])
                self.assertEqual(nightly['full_go_packages'], [])
                self.assertEqual(merge['full_go_extra_seconds'], 0)

    def test_docs_only_selects_no_extra_go(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            self.fixture(root)
            with patch('plan.ROOT', root), patch('plan.command', return_value='tags'), \
                 patch('plan.source_readers.select', return_value={}):
                result = plan.make_plan(['README.md'])
            self.assertEqual((result['packages'], result['full_go_packages'], result['full_go_extra_seconds']), ([], [], 0))


class MixedRunnerModes(unittest.TestCase):
    def test_complete_and_split_packages_keep_their_own_short_flag(self):
        names = ['full', 'short', 'partial_full', 'partial_short']
        selected = [{'module': 'server', 'package': p, 'name': 'TestA'} for p in names]
        inventory = selected + [{'module': 'server', 'package': p, 'name': 'TestOther'} for p in names[2:]]
        for tier in ['short', 'merge', 'nightly']:
            with self.subTest(tier=tier), tempfile.TemporaryDirectory() as d:
                root = Path(d)
                (root / 'ci').mkdir()
                (root / 'ci-tools').mkdir()
                (root / 'ci-tools/portico-server').touch()
                (root / 'ci/quarantine.json').write_text('{"entries": []}')
                selection = {'tier': tier, 'tags': 'tags', 'all_inventory': inventory, 'shards': [selected],
                             'full_go_packages': ['full', 'partial_full']}
                commands = []
                def execute(args, **kwargs):
                    commands.append(args)
                    packages = [x for x in args if x in names]
                    events = [e for p in packages for e in [
                        {'Package': p, 'Test': 'TestA', 'Action': 'pass'}, {'Package': p, 'Action': 'pass'}]]
                    return subprocess.CompletedProcess(args, 0, '\n'.join(map(json.dumps, events)), '')
                with patch('run.ROOT', root), patch('run.validate_tags'), patch('run.run_go', side_effect=execute), \
                     patch.dict('os.environ', {'PORTICO_CI_GO_PACKAGE_JOBS': '2'}), \
                     contextlib.redirect_stdout(io.StringIO()):
                    run.go(selection, 0)
                covered = []
                for args in commands:
                    packages = [x for x in args if x in names]
                    covered += packages
                    for package in packages:
                        self.assertEqual('-short' in args, tier == 'short' and package in ['short', 'partial_short'])
                    self.assertEqual('-run' in args, packages[0].startswith('partial_'))
                    self.assertIn('-timeout=780s', args)
                self.assertCountEqual(covered, names)
                if tier != 'short':
                    self.assertEqual(len(commands), 3)  # unchanged complete batch + two split processes


if __name__ == '__main__':
    unittest.main()
