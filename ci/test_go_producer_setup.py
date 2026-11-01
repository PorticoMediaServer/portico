import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from run import js, prepare_httpapi_producer
from js_shards import receipt_name


CONSUMERS = ['packages/contracts/test/invitation-producer-contract.test.ts',
             'packages/contracts/test/reset-producer-contract.test.ts']


class ProducerSetup(unittest.TestCase):
    def test_compile_once_when_either_or_both_consumers_are_selected(self):
        for selected in [CONSUMERS[:1], CONSUMERS[1:], CONSUMERS]:
            with tempfile.TemporaryDirectory() as directory, \
                 patch.dict(os.environ, {}, clear=True), patch('run.execute') as execute:
                prepare_httpapi_producer(selected, directory)
                execute.assert_called_once()
                self.assertEqual(execute.call_args.args[0],
                                 ['go', 'test', '-c', '-o', str(Path(directory) / 'httpapi-producer.test'),
                                  './internal/httpapi'])
                self.assertEqual(os.environ['PORTICO_HTTPAPI_PRODUCER_BINARY'],
                                 str(Path(directory) / 'httpapi-producer.test'))
        with patch('run.execute') as execute:
            prepare_httpapi_producer(['packages/client-core/test/a.test.ts'], '/unused')
            execute.assert_not_called()

    def run_shard(self, compilation_fails=False):
        plan = {'head': 'test-head', 'js': [{'component': 'packages', 'files': CONSUMERS,
                'shards': [CONSUMERS], 'static_roots': ['packages/contracts']}]}
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            seen = []
            def execute(args, *rest):
                if args[:3] == ['go', 'test', '-c']:
                    seen.append('compile')
                    if compilation_fails: raise subprocess.CalledProcessError(1, args)
                    Path(args[args.index('-o') + 1]).touch()
            def node(args, cwd, env):
                seen.append('node')
                binary = Path(env['PORTICO_HTTPAPI_PRODUCER_BINARY'])
                self.assertTrue(binary.is_file())
                self.assertEqual(seen, ['compile', 'node'])
                self.assertIn('--test-timeout=120000', args)
                self.assertEqual(args[-2:], ['test/invitation-producer-contract.test.ts',
                                             'test/reset-producer-contract.test.ts'])
                target = root / 'ci-results/js/packages-0-packages-contracts.jsonl'
                self.assertIn('--test-reporter-destination=' + str(target), args)
                target.write_text(''.join(json.dumps({'file': f, 'status': 'pass', 'seconds': 1}) + '\n'
                                          for f in CONSUMERS))
                return 0
            with patch('run.ROOT', root), patch('run.command_head', return_value=plan['head']), \
                 patch('run.subprocess.check_output', return_value='v24'), patch('run.fixture_producers'), \
                 patch('run.execute', side_effect=execute), patch('run.node_process', side_effect=node) as launch, \
                 patch.dict(os.environ, {'PORTICO_HTTPAPI_PRODUCER_BINARY': '/preexisting'}):
                if compilation_fails:
                    with self.assertRaises(subprocess.CalledProcessError): js(plan, 'packages', 0)
                    launch.assert_not_called()
                else:
                    js(plan, 'packages', 0)
                self.assertEqual(os.environ['PORTICO_HTTPAPI_PRODUCER_BINARY'], '/preexisting')
            record = json.loads((root / 'ci-results/js' / receipt_name('packages', 0)).read_text())
            self.assertEqual(record['assigned'], CONSUMERS)
            self.assertEqual(record['returncode'], 1 if compilation_fails else 0)
            if not compilation_fails: self.assertIn('producer_compile', record['steps_seconds'])
            self.assertEqual(list((root / 'ci-results/tmp').iterdir()), [])

    def test_binary_reused_with_same_files_deadline_receipts_and_environment_restoration(self):
        self.run_shard()

    def test_compiler_failure_cannot_become_a_passing_receipt(self):
        self.run_shard(compilation_fails=True)
