import tempfile
import os
from pathlib import Path
import unittest
from unittest.mock import patch
from plan import event_tier
from js_affected import select

class Correctness(unittest.TestCase):
    def test_event_tiers(self):
        self.assertEqual(event_tier('merge_group','',False),('merge',False))
        self.assertEqual(event_tier('pull_request','',False),('short',False))
        self.assertEqual(event_tier('workflow_dispatch','short',True),('merge',True))
        for event,tier,full in [('merge_group','short',False),('merge_group','merge',True),('push','merge',False),('pull_request','short',True)]:
            with self.assertRaises(ValueError): event_tier(event,tier,full)
    def test_graph_consumers_alias_dynamic_and_security(self):
        with tempfile.TemporaryDirectory() as d:
            root=Path(d); (root/'scripts').mkdir()
            source=Path(__file__).resolve().parents[1]/'scripts/ci-affected.py'
            (root/'scripts/ci-affected.py').write_text(source.read_text())
            data={'packages/client-core/src/item.ts':'export const x=1;',
                  'apps/native/src/use.ts':"import {x} from '@core/item';",
                  'apps/native/test/changed.test.ts':"import '../src/use.ts';",
                  'apps/native/test/dynamic.test.ts':"import('../src/use.ts');",
                  'apps/native/test/security.test.ts':'',
                  'apps/native/test/unrelated.test.ts':''}
            for name,text in data.items():
                p=root/name;p.parent.mkdir(parents=True,exist_ok=True);p.write_text(text)
            candidates=[p for p in data if '.test.' in p]
            got=select(root,['packages/client-core/src/item.ts'],candidates)
            self.assertEqual(set(got),set(candidates)-{'apps/native/test/unrelated.test.ts'})
            self.assertIn('apps/native/test/changed.test.ts',select(root,['apps/native/test/changed.test.ts'],candidates))
            self.assertEqual(select(root,['apps/native/src/deleted.ts'],candidates),candidates)
    def test_computed_imports_retain_exact_owner_without_neighbor_prefixes(self):
        with tempfile.TemporaryDirectory() as d:
            root=Path(d); (root/'scripts').mkdir()
            source=Path(__file__).resolve().parents[1]/'scripts/ci-affected.py'
            (root/'scripts/ci-affected.py').write_text(source.read_text())
            owners=['apps/native','apps/native-extra','packages/core','packages/core-extra','web','website']
            candidates=[owner+'/test/consumer.test.ts' for owner in owners]
            for owner in owners:
                for name,text in [(owner+'/src/deep/input.ts','export const value=1;'),
                                  (owner+'/test/consumer.test.ts','import(target);')]:
                    p=root/name;p.parent.mkdir(parents=True,exist_ok=True);p.write_text(text)
            for owner in owners:
                with self.subTest(owner=owner):
                    diagnostics={}
                    got=select(root,[owner+'/src/deep/input.ts'],candidates,diagnostics)
                    self.assertEqual(got,[owner+'/test/consumer.test.ts'])
                    self.assertNotIn('fallback',diagnostics)
    def test_wrong_queue_checkout_rejected(self):
        from plan import base_head
        with patch('plan.command',return_value='c'*40):
            with self.assertRaisesRegex(ValueError,'checkout'):
                base_head({'merge_group':{'base_sha':'a'*40,'head_sha':'b'*40}}, {'GITHUB_EVENT_NAME':'merge_group'})
    def test_full_recording_not_short(self):
        from run import fixture_producers
        with patch.dict(os.environ), tempfile.TemporaryDirectory() as d, patch('run.execute') as execute, patch('run.Path.glob',return_value=[Path('record.json')]):
            fixture_producers(['final-batch/concurrency-memory-limit.test.ts'],d)
            args=execute.call_args.args[0]
            self.assertNotIn('-short',args)
            self.assertIn('^TestConcurrencyDiagnosticsMemoryLimitWire$',args)
