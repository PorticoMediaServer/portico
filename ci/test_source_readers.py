import json
from pathlib import Path
import re
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import plan
import source_readers as sr
from run import batches

DATA = sr.load()
ROOT = sr.ROOT
SCAN = sr.scan()

def tracked():
    return subprocess.check_output(['git', 'ls-files', '-z'], cwd=ROOT, text=True).split('\0')

def defined(directory):
    return {m[1] for f in directory.glob('*_test.go') for m in re.finditer(r'^func\s+((?:Test|Fuzz)\w+)\(', f.read_text(), re.M)}

class Manifest(unittest.TestCase):
    """ci/source-readers.json lists every Go test that reads files outside its package, and stays true to the tests."""

    def test_entries_name_real_tests_files_and_inputs(self):
        files = tracked()
        for entry in DATA['readers']:
            directory = ROOT / entry['package']
            self.assertTrue(list(directory.glob('*.go')), entry['package'] + ' is not a Go package directory')
            self.assertEqual(entry['tests'], sorted(set(entry['tests'])), entry['package'] + ': tests must be sorted and unique')
            for name in entry['tests']: self.assertIn(name, defined(directory), entry['package'] + ' has no test ' + name)
            for evidence in entry['files']:
                self.assertEqual(str(Path(evidence).parent), entry['package'], evidence + ' is not in its package')
                self.assertIn(evidence, SCAN, evidence + ' no longer reads outside its package: drop it from the manifest')
            for pattern in entry['reads']:
                if pattern.endswith('/'): self.assertTrue((ROOT / pattern).is_dir(), pattern + ' is not a directory')
                else: self.assertTrue(any(sr.reads({'reads': [pattern]}, f) for f in files), pattern + ' matches no tracked file')

    def test_every_test_file_that_reads_outside_its_package_is_declared_or_reviewed(self):
        declared = {f for e in DATA['readers'] for f in e['files']}
        for name, reason in DATA['reviewed'].items():
            self.assertTrue(reason, name + ' needs a reason')
            self.assertNotIn(name, declared, name + ' is both declared and reviewed')
            self.assertIn(name, SCAN, name + ' no longer reads outside its package: drop it from reviewed')
        unaccounted = sorted(set(SCAN) - declared - set(DATA['reviewed']))
        self.assertEqual(unaccounted, [], 'These test files read outside their package. Add the tests that read to a ci/source-readers.json '
                         'entry (with the files they read), or list the file under "reviewed" with the reason it is not a reader.')

    def test_declared_tests_cover_every_test_that_reaches_a_read(self):
        for entry in DATA['readers']:
            reached = sr.marked_tests(ROOT / entry['package'], [ROOT / f for f in entry['files']])
            self.assertEqual(sorted(reached - set(entry['tests'])), [], entry['package'] + ': tests reach a reading helper but are not declared')

class Selection(unittest.TestCase):
    def test_a_change_to_an_ingestion_file_selects_the_census(self):
        chosen = sr.select(['server/internal/ingestion/path_presence.go'], DATA)
        self.assertIn('TestSeparateFolderIndexCensusClassifiesInventoryObjectWrites', chosen['server/internal/persistence'])
        self.assertIn('server/internal/supervise', chosen); self.assertIn('server/internal/mediaexec', chosen)
        self.assertEqual(chosen['server/internal/persistence']['TestSeparateFolderIndexCensusClassifiesInventoryObjectWrites'], ['server/internal/ingestion/path_presence.go'])

    def test_deleted_files_select_too_but_tests_and_testdata_and_docs_do_not(self):
        for path in ['server/internal/ingestion/x_test.go', 'server/internal/ingestion/testdata/a.go', 'server/internal/ingestion/README.md', 'web/src/a.ts']:
            self.assertNotIn('server/internal/persistence', sr.select([path], DATA), path)
        self.assertIn('server/internal/persistence', sr.select(['server/cmd/server/gone.go'], DATA))

    def test_files_outside_the_server_select_the_tests_that_read_them(self):
        self.assertEqual(set(sr.select(['packages/i18n/src/catalog/en-US/settings.ts'], DATA)), {'server/cmd/server'})
        self.assertIn('server/internal/disc', sr.select(['fixtures/discs/manifest.json'], DATA))
        self.assertIn('server/internal/operations', sr.select(['packages/client-core/src/preference-effects.ts'], DATA))
        self.assertEqual(sr.select(['packages/client-core/src/other.mjs'], DATA), {})

    def test_malformed_entries_are_refused(self):
        with tempfile.TemporaryDirectory() as d:
            path = Path(d) / 'm.json'; path.write_text(json.dumps({'readers': [{'package': 'p', 'tests': ['TestA'], 'reads': ['x/'], 'files': ['p/a_test.go']}]}))
            with self.assertRaisesRegex(ValueError, 'why'): sr.load(path)

    def test_the_heuristic_ignores_traversal_fixtures_and_finds_real_reads(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d); (root / 'a').mkdir(); (root / 'b').mkdir(); (root / 'b/x.go').write_text('package b')
            source = root / 'a/a_test.go'
            source.write_text('package a\nfunc TestA(t *testing.T) {\n\tos.ReadFile("../b/x.go")\n\tDelete("../escape")\n\tfilepath.Join(root, "..", "outside")\n}\n'
                              'func helper() { filepath.Abs("../..") }\nfunc TestB(t *testing.T) { helper() }\n')
            self.assertEqual(sr.marker_lines(source), [3, 7])
            self.assertEqual(sr.marked_tests(root / 'a', [source]), {'TestA', 'TestB'})

class Planning(unittest.TestCase):
    """The planner adds the declared tests, and only those, for a package the import graph does not reach."""

    def listed(self, module):
        if module != 'server': return ''
        stream = []
        for package in sorted({e['package'] for e in DATA['readers']} | {'server/internal/ingestion'}):
            directory = ROOT / package
            stream.append({'ImportPath': 'github.com/PorticoMediaServer/portico/' + package, 'Dir': str(directory),
                           'GoFiles': sorted(f.name for f in directory.glob('*.go') if not f.name.endswith('_test.go')),
                           'TestGoFiles': sorted(f.name for f in directory.glob('*_test.go'))})
        return ''.join(json.dumps(p) + '\n' for p in stream)

    def fake(self, args, cwd=None):
        if args[0] == 'python3': return 'tags\n'
        return self.listed(Path(cwd).name)

    def plan(self, paths, tier='short', owned=True):
        with patch('plan.command', side_effect=self.fake), patch('plan.components', return_value=([], owned)):
            return plan.make_plan(paths, tier=tier)

    def test_ingestion_change_runs_the_census_and_the_other_source_guards_only_for_those_packages(self):
        for tier in ['short', 'merge']:
            result = self.plan(['server/internal/ingestion/path_presence.go'], tier)
            names = {(t['package'].rsplit('/', 1)[-1], t['name']) for t in result['inventory']}
            self.assertIn(('persistence', 'TestSeparateFolderIndexCensusClassifiesInventoryObjectWrites'), names)
            self.assertIn(('supervise', 'TestEveryGoroutineIsStartedThroughThisPackage'), names)
            # persistence was chosen only for its readers: its other tests are not selected, but stay in the full inventory
            self.assertNotIn(('persistence', 'TestSeparateFolderMinimalCursorSchema'), names)
            self.assertIn('TestSeparateFolderMinimalCursorSchema', {t['name'] for t in result['all_inventory'] if t['package'].endswith('/persistence')})
            self.assertTrue(any(x['name'] == 'TestSeparateFolderIndexCensusClassifiesInventoryObjectWrites' for x in result['source_reader_tests']))
            self.assertTrue(result['selection_diagnostics']['source_readers'])
            partial = [b for b in batches(result, [t for t in result['inventory']]) if not b[2]]
            self.assertTrue(any(b[1][0][0].endswith('/persistence') for b in partial), 'persistence runs by -run selection, not as a whole package')

    def test_a_package_already_selected_by_imports_runs_whole(self):
        with patch('plan.closure', side_effect=lambda packages, seeds: {p['ImportPath'] for p in packages}):
            result = self.plan(['server/internal/ingestion/path_presence.go'])
        self.assertIn('TestSeparateFolderMinimalCursorSchema', {t['name'] for t in result['inventory']})
        self.assertEqual(result['source_reader_tests'], [])

    def test_a_non_go_file_selects_only_its_reader_without_a_full_go_inventory(self):
        result = self.plan(['packages/i18n/src/catalog/en-US/settings.ts'], owned=False)
        self.assertEqual([(t['package'].rsplit('/', 1)[-1], t['name']) for t in result['inventory']], [('server', 'TestRecoveryCopyMatchesStringCatalogue')])
        self.assertEqual(result['selection_diagnostics']['go'], 'no Go change selected')
        self.assertNotIn('full Go inventory', json.dumps(result['selection_diagnostics']))

    def test_a_change_no_reader_reads_selects_no_go(self):
        result = self.plan(['web/src/a.ts'], owned=False)
        self.assertEqual((result['packages'], result['inventory'], result['source_reader_tests']), ([], [], []))

    def test_a_full_plan_ignores_the_manifest(self):
        with patch('plan.command', side_effect=self.fake), patch('plan.components', return_value=([], True)):
            result = plan.make_plan([], full=True, tier='merge')
        self.assertEqual(result['source_reader_tests'], [])
        self.assertIn('TestSeparateFolderMinimalCursorSchema', {t['name'] for t in result['inventory']})

    def test_a_declared_test_missing_from_the_build_fails_the_plan(self):
        broken = {'readers': [{'package': 'server/internal/supervise', 'tests': ['TestNoSuchThing'], 'reads': ['server/*.go'], 'files': ['x'], 'why': 'x'}], 'reviewed': {}}
        with patch('source_readers.load', return_value=broken):
            with self.assertRaisesRegex(ValueError, 'TestNoSuchThing'): self.plan(['server/internal/ingestion/path_presence.go'])

    def test_a_declared_directory_that_is_not_a_go_package_fails_the_plan(self):
        broken = {'readers': [{'package': 'server/internal/gone', 'tests': ['TestA'], 'reads': ['server/*.go'], 'files': ['x'], 'why': 'x'}], 'reviewed': {}}
        with patch('source_readers.load', return_value=broken):
            with self.assertRaisesRegex(ValueError, 'not a Go package'): self.plan(['server/internal/ingestion/path_presence.go'])

if __name__ == '__main__': unittest.main()
