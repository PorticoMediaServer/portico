import copy
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from js_shards import balance_files, digest, extend_selection, reconcile, validate_selection
from plan import ROOT
from run import aggregate, js, js_static, node_process
from update_js_durations import update


def plan():
    return {'head': 'a' * 40, 'js': extend_selection([{'component': 'packages', 'files': [
        'packages/a/test/a.test.ts', 'packages/b/test/b.test.ts', 'packages/a/test/c.test.ts']}], {})}


def receipts(p, directory):
    for item in p['js']:
        for index, files in enumerate(item['shards']):
            record = {'version': 1, 'head': p['head'], 'plan_sha256': digest(p),
                      'component': item['component'], 'index': index, 'assigned': files,
                      'returncode': 0, 'files': [{'file': f, 'status': 'pass', 'seconds': .1} for f in files]}
            (directory / f'{index}.json').write_text(json.dumps(record))


class Allocation(unittest.TestCase):
    def test_balanced_deterministic_unknown_and_exact_union(self):
        files = ['a', 'b', 'c', 'new']
        bins, costs = balance_files(files, {'a': 120, 'b': 60, 'c': 30}, 3)
        self.assertEqual((bins, costs), balance_files(list(reversed(files)), {'c': 30, 'b': 60, 'a': 120}, 3))
        self.assertEqual(sorted(f for b in bins for f in b), sorted(files))
        self.assertEqual(sum(costs), 330)  # unknown gets measured P90 (120)
        self.assertEqual(balance_files(['new'], {}, 4), ([['new']], [30.0]))
        self.assertEqual(balance_files(['new'], {'known':90}, 4), ([['new']], [90.0]))
        for durations in [{'a': float('nan')}, {'a': -1}, {'a': True}, {'a': 'bad'}]:
            with self.assertRaises(ValueError): balance_files(['a'], durations, 1)
        with self.assertRaises(ValueError): balance_files(['a', 'a'], {}, 2)

    def test_static_roots_and_union_cannot_drift(self):
        p = plan()
        self.assertEqual(p['js'][0]['static_roots'], ['packages/a', 'packages/b'])
        for mutate in [lambda x: x['shards'].append(x['shards'][0]),
                       lambda x: x['shards'].pop(), lambda x: x['static_roots'].pop()]:
            broken = copy.deepcopy(p['js']); mutate(broken[0])
            with self.assertRaises(ValueError): validate_selection(broken)
        website = extend_selection([{'component': 'website', 'files': []}], {})[0]
        self.assertEqual((website['shards'], website['static_roots']), ([], ['website']))

    def test_static_commands_once_per_selected_root_and_rust_website(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            for group in ['packages/a', 'packages/b']:
                (root/group).mkdir(parents=True)
                (root/group/'package.json').write_text('{"scripts":{"lint:hooks":"run","tokens":"run"}}')
                (root/group/'tsconfig.json').write_text('{}')
            with patch('run.ROOT', root), patch('run.execute') as execute:
                js_static(plan(), 'packages')
                commands = [call.args for call in execute.call_args_list]
                self.assertEqual(sum(c[0] == ['npx', '--no-install', 'tsc', '--noEmit'] for c in commands), 2)
                self.assertEqual(sum(c[0] == ['npm', 'run', 'tokens'] for c in commands), 2)
                self.assertEqual(sum(c[0] == ['npm', 'run', 'build'] for c in commands), 1)
            with patch('run.execute') as execute:
                js_static({'js': extend_selection([{'component': 'website', 'files': []}], {})}, 'website')
                self.assertEqual(execute.call_args.args[0], ['npm', 'run', 'check'])

    def test_desktop_rust_remains_once(self):
        p={'js':extend_selection([{'component':'apps/desktop','files':['apps/desktop/test/a.test.ts']}],{})}
        with patch('run.execute') as execute:
            js_static(p,'apps/desktop')
            self.assertEqual(sum(c.args[0]==['npm','run','test:rust'] for c in execute.call_args_list),1)



class Accounting(unittest.TestCase):
    def test_receipt_mutations_fail_closed(self):
        p = plan()
        mutations = [lambda r: r.update(head='b'*40), lambda r: r.update(plan_sha256='0'*64),
                     lambda r: r.update(index=100), lambda r: r.update(returncode=1),
                     lambda r: r['files'].clear(), lambda r: r['files'].append(r['files'][0]),
                     lambda r: r['files'][0].update(file='unexpected'),
                     lambda r: r['files'][0].update(status='skip'),
                     lambda r: r['files'][0].update(seconds=float('nan')),
                     lambda r: r['assigned'].append('unexpected')]
        for mutate in mutations:
            with self.subTest(mutate=mutate), tempfile.TemporaryDirectory() as d:
                folder = Path(d); receipts(p, folder)
                target = folder/'0.json'; data = json.loads(target.read_text()); mutate(data)
                target.write_text(json.dumps(data))
                with self.assertRaises(ValueError): reconcile(p, folder)
        with tempfile.TemporaryDirectory() as d:
            folder = Path(d); receipts(p, folder)
            self.assertEqual(len(reconcile(p, folder)), 3)
            (folder/'duplicate.json').write_text((folder/'0.json').read_text())
            with self.assertRaises(ValueError): reconcile(p, folder)
            (folder/'duplicate.json').unlink(); (folder/'0.json').unlink()
            with self.assertRaises(ValueError): reconcile(p, folder)
            (folder/'0.json').write_text('{')
            with self.assertRaises(ValueError): reconcile(p, folder)

    def test_required_static_outcome_and_receipts(self):
        p = dict(plan(), packages=[])
        needs = {k: {'result': 'success' if k in ['planner', 'hygiene', 'js', 'js_static'] else 'skipped'}
                 for k in ['planner', 'hygiene', 'go_build', 'go_tests', 'js', 'js_static', 'android']}
        with tempfile.TemporaryDirectory() as d:
            root = Path(d); folder = root/'ci-results/js'; folder.mkdir(parents=True); receipts(p, folder)
            with patch('run.ROOT', root):
                aggregate(p, needs)
                for state in ['failure', 'cancelled', 'skipped', None]:
                    needs['js_static']['result'] = state
                    with self.assertRaises(ValueError): aggregate(p, needs)

    def test_absent_file_terminal_blocks_runner_even_on_zero_exit(self):
        p=plan()
        with tempfile.TemporaryDirectory() as d:
            root=Path(d)
            with patch('run.ROOT',root),patch('run.command_head',return_value=p['head']),patch('run.execute'),patch('run.fixture_producers'),patch('run.node_process',return_value=0):
                with self.assertRaisesRegex(ValueError,'did not report'):
                    js(p,'packages',0)
            record=json.loads(next((root/'ci-results/js').glob('*.json')).read_text())
            self.assertEqual(record['files'],[])
            with self.assertRaises(ValueError): reconcile(p,root/'ci-results/js')

    def test_updater_retains_unknown_measurements_and_provenance(self):
        p=plan()
        records={0:{'node_version':'v24','files':[{'file':'new.test.ts','seconds':2.5}]}}
        data=update({'seconds':{'old.test.ts':4},'provenance':[]},p,records,'local focused selection')
        self.assertEqual(data['seconds'],{'new.test.ts':2.5,'old.test.ts':4})
        self.assertEqual(data['provenance'][0]['head'],p['head'])



class Reporter(unittest.TestCase):
    def run_fixture(self, source, timeout='3000'):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d); file = root/'case.test.mjs'; file.write_text(source)
            output = root/'events.jsonl'
            result = subprocess.run(['node', '--test', '--test-timeout='+timeout,
                                     '--test-reporter='+str(ROOT/'ci/js-reporter.mjs'),
                                     '--test-reporter-destination='+str(output), file.name], cwd=root,
                                    env=dict(os.environ, PORTICO_CI_ROOT=str(root)), capture_output=True, text=True, timeout=10)
            records = [json.loads(line) for line in output.read_text().splitlines()]
            self.assertEqual(len(records), 1, (result.stdout, result.stderr, records))
            self.assertEqual(records[0]['file'], 'case.test.mjs')
            return result.returncode, records[0]

    def test_real_parent_file_terminals(self):
        for source in ["import {test} from 'node:test'; test('pass',()=>{}); test.skip('skip',()=>{});",
                       "import {test} from 'node:test'; test('nested',async t=>{await t.test('child',()=>{});});",
                       "console.log('file without test declarations still executes');"]:
            code, record = self.run_fixture(source)
            self.assertEqual((code, record['status']), (0, 'pass'))
            self.assertGreaterEqual(record['seconds'], 0)
        for source in ["throw new Error('import failed');",
                       "import {test} from 'node:test'; test('fails',()=>{throw new Error('assertion');});",
                       "process.exit(2);", "process.kill(process.pid, 'SIGKILL');"]:
            code, record = self.run_fixture(source)
            self.assertNotEqual(code, 0); self.assertEqual(record['status'], 'fail')
        code, record = self.run_fixture("import {test} from 'node:test'; test('hang',{timeout:20},()=>new Promise(resolve=>setTimeout(resolve,100)));", '3000')
        self.assertNotEqual(code, 0); self.assertEqual(record['status'], 'fail')

class ProcessCleanup(unittest.TestCase):
    def test_external_timeout_stops_the_process_group(self):
        with self.assertRaises(subprocess.TimeoutExpired):
            node_process(['node','-e','setInterval(()=>{},1000)'],ROOT,dict(os.environ),timeout=.1)
