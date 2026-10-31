import json
import unittest
from pathlib import Path
from plan import ROOT, components, android_selected, go_seeds, balance, guarded_costs
from run import batches, classify

class Redesign(unittest.TestCase):
    def test_go_only_and_android_routing(self):
        self.assertEqual(components(['server/internal/auth/a.go']),([],True))
        self.assertFalse(android_selected(['server/a.go','apps/native/src/ui.tsx']))
        self.assertTrue(android_selected(['apps/native/android/app/build.gradle']))
        self.assertTrue(android_selected([],True))
    def test_mixed_diff_does_not_broaden_go(self):
        ps=[{'ImportPath':'a','Dir':str(ROOT/'server/a')},{'ImportPath':'b','Dir':str(ROOT/'server/b')}]
        for suffix in ['README.md','web/main.ts','server/readme.md']:
            self.assertEqual(go_seeds(ps,['server/a/file.go',suffix]),{'a'})
        self.assertEqual(go_seeds(ps,['server/a/testdata/input']),{'a'})
        self.assertEqual(go_seeds(ps,['server/go.mod']),{'a','b'})
        self.assertEqual(go_seeds(ps,['server/a/unknown.cfg']),{'a','b'})
    def test_batch_only_complete_packages(self):
        tests=[{'module':'server','package':p,'name':n} for p,n in [('a','TestA'),('b','TestB'),('b','TestNightly'),('c','TestC')]]
        calls=batches({'all_inventory':tests},[tests[0],tests[1],tests[3]])
        self.assertEqual([p for p,ts in calls[0][1]],['a','c'])
        self.assertTrue(calls[0][2]);self.assertFalse(calls[1][2])
        self.assertEqual(calls[1][1][0][0],'b')
    def test_real_passing_timeout_fixture(self):
        fixture=json.loads((ROOT/'ci/fixtures/passing-timeout.json').read_text())
        events=fixture['events']; names={e['Test'] for e in events if e.get('Test')}
        self.assertEqual(classify(events,names,0,set()),'pass')
        self.assertEqual(len(fixture['provenance']['sha256']),64)
    def test_runtime_markers_and_unfinished(self):
        good=[{'Test':'TestA','Action':'pass'},{'Action':'pass'}]
        for output in ['timed out','signal: logged','build failed logged','panic: logged']:
            self.assertEqual(classify([{'Test':'TestA','Output':output}]+good,['TestA'],0,set()),'pass')
        for event in [{'Output':'WARNING: DATA RACE'}, {'Output':'panic: test timed out after 10m'}, {'Action':'build-fail'}]:
            self.assertEqual(classify(good+[event],['TestA'],0,set()),'blocked')
        self.assertEqual(classify(good,['TestMissing'],0,set()),'blocked')
    def test_deterministic_ten_worker_cap(self):
        tests=[{'package':'p','name':str(i)} for i in range(50)]
        durations={'p.'+str(i):50 for i in range(50)}
        self.assertEqual(balance(tests,durations),balance(list(reversed(tests)),durations))
        self.assertEqual(len(balance(tests,durations)[0]),10)
    def test_nightly_exact_unique_owners(self):
        tiers=json.loads((ROOT/'ci/tiers.json').read_text())
        keys=[(t['package'],t['name']) for lane in tiers['nightly_lanes'].values() for t in lane]
        self.assertEqual(len(keys),len(set(keys)))
        self.assertEqual(len(keys),6)

class BatchExecution(unittest.TestCase):
    def test_batched_package_terminals_and_diagnostic_stderr(self):
        import tempfile
        from unittest.mock import patch
        from types import SimpleNamespace
        from run import go
        tests=[{'module':'server','package':p,'name':'TestA'} for p in ['a','b']]
        plan={'tags':'tags','tier':'merge','shards':[tests],'all_inventory':tests}
        with tempfile.TemporaryDirectory() as d:
            root=Path(d);(root/'ci').mkdir();(root/'ci-tools').mkdir()
            (root/'ci/quarantine.json').write_text('{"entries":[]}')
            (root/'ci-tools/portico-server').touch()
            events=[{'Package':p,'Test':'TestA','Action':'pass'} for p in ['a','b']]+[{'Package':p,'Action':'pass'} for p in ['a','b']]
            result=SimpleNamespace(stdout='\n'.join(map(json.dumps,events)),stderr='go: downloading example dependency',returncode=0)
            with patch('run.ROOT',root),patch('run.validate_tags'),patch.dict('os.environ',{'PORTICO_CI_GO_PACKAGE_JOBS':'4'}),patch('run.subprocess.run',return_value=result) as run:
                go(plan,0)
                args=run.call_args.args[0]
                self.assertNotIn('-run',args);self.assertIn('-p=4',args)
                self.assertEqual(args[-2:],['a','b'])
                with patch.dict('os.environ',{'PORTICO_CI_GO_PACKAGE_JOBS':'1'}):
                    go(plan,0)
                    self.assertIn('-p=1',run.call_args.args[0])
                    self.assertEqual(run.call_args.args[0][-2:],['a','b'])
                result.returncode=1
                with self.assertRaises(SystemExit): go(plan,0)
