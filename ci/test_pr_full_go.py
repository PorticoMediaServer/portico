import contextlib
import io
import json
from pathlib import Path
import subprocess
import tempfile
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

    def test_promoted_prs_can_use_twenty_shards_without_changing_queue_cap(self):
        tests = [{'package': 'p', 'name': 'Test' + str(i)} for i in range(200)]
        costs = {'p.' + t['name']: 20 for t in tests}
        self.assertEqual(len(plan.balance_short_censuses(tests, costs, max_count=20)[0]), 20)
        self.assertEqual(len(plan.balance(tests, costs)[0]), 10)


class FullPlan(unittest.TestCase):
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
