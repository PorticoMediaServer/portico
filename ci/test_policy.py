import datetime
import unittest
from plan import balance, closure, components, decode_stream
from run import aggregate, classify, policy

class PolicyTests(unittest.TestCase):
    def test_reverse_transitive_and_test_imports(self):
        ps=[{'ImportPath':'a'},{'ImportPath':'b','Imports':['a']},{'ImportPath':'c','TestImports':['b']},{'ImportPath':'unrelated'}]
        self.assertEqual(closure(ps,{'a'}),{'a','b','c'})
    def test_lpt_exact_once_and_unknown(self):
        tests=[{'package':'p','name':x} for x in ['TestA','ExampleB','FuzzC','TestNew']]
        bins,cost=balance(tests,{'p.TestA':300,'p.ExampleB':120})
        self.assertEqual(sum(map(len,bins)),4); self.assertEqual(sum(cost),422)
        self.assertEqual(len({t['name'] for b in bins for t in b}),4)
    def test_deleted_and_unknown_select(self):
        self.assertTrue(components(['server/deleted/file.go'])[1])
        self.assertEqual(components(['unknown.config']),components([],True))
        self.assertIn('apps/native',components(['packages/client-core/src/auth.ts'])[0])
    def test_stream(self): self.assertEqual(decode_stream('{}\n{}'),[{},{}])
    def test_aggregate_absent_cancelled_skipped(self):
        p={'packages':[],'js':[]}; n={k:{'result':v} for k,v in [('planner','success'),('hygiene','success'),('go_build','skipped'),('go_tests','skipped'),('js','skipped'),('android','skipped')]}
        aggregate(p,n)
        for state in ['cancelled','failure',None,'success']:
            bad=dict(n); bad['go_tests']={'result':state}
            with self.assertRaises(ValueError): aggregate(p,bad)
    def test_exact_quarantine_no_infra_waiver(self):
        events=[{'Test':'TestKnown/child','Action':'fail'},{'Test':'TestKnown','Action':'fail'},{'Action':'fail'}]
        self.assertEqual(classify(events,['TestKnown'],1,{'TestKnown'}),'quarantined')
        for extra in [{'Test':'TestOther','Action':'fail'},{'Output':'panic: oops'},{'Output':'panic: test timed out after 10m'}]:
            self.assertEqual(classify(events+[extra],['TestKnown'],1,{'TestKnown'}),'blocked')
        self.assertEqual(classify(events,['TestMissing'],1,{'TestKnown'}),'blocked')
        self.assertEqual(classify(events,['TestKnown'],2,{'TestKnown'}),'blocked')
    def test_policy_expiry_owner_approval(self):
        entry={'package':'p','test':'TestA','owner':'Justin Ehler','expires':'2026-11-02','reason':'repair'}
        for status,day in [('proposal-inactive','2026-10-29'),('approved','2026-11-03')]:
            with self.assertRaises(ValueError): policy({'status':status,'entries':[entry]},datetime.date.fromisoformat(day))
        self.assertEqual(len(policy({'status':'approved','entries':[entry]},datetime.date(2026,10,29))),1)
if __name__=='__main__': unittest.main()

class BaseTests(unittest.TestCase):
    def test_pr_merge_base(self):
        from unittest.mock import patch
        from plan import base_head
        with patch('plan.command', side_effect=['head\n','base\n']) as call:
            self.assertEqual(base_head({'pull_request':{'base':{'sha':'target'}}},{'GITHUB_EVENT_NAME':'pull_request'}),('base','head'))
            self.assertEqual(call.call_args.args[0],['git','merge-base','head','target'])
    def test_push_zero_rejected_force_base_retained(self):
        from unittest.mock import patch
        from plan import base_head
        with patch('plan.command',return_value='head\n'):
            with self.assertRaises(ValueError): base_head({'before':'0'*40},{'GITHUB_EVENT_NAME':'push'})
        with patch('plan.command',side_effect=['head\n','']):
            self.assertEqual(base_head({'before':'a'*40},{'GITHUB_EVENT_NAME':'push'}),('a'*40,'head'))
    def test_empty_js_list_fails_before_execution(self):
        from run import js
        with self.assertRaises(ValueError): js({'js':[{'component':'web','files':[]}]},'web')

class ActivePolicyTests(unittest.TestCase):
    def test_reviewed_corrections(self):
        import json
        from plan import ROOT
        tiers=json.loads((ROOT/'ci/tiers.json').read_text())
        excluded={e['name'] for e in tiers['nightly_only']}
        self.assertEqual(len(excluded),5)
        self.assertNotIn('TestReadPathGoldenEquivalence',excluded)
        self.assertNotIn('TestKeysetPagesDoNotRescanTheirPrefix',excluded)
        self.assertNotIn('TestBulkBuildDerivesWhatIncrementalBuildDoes',excluded)
        q=policy(json.loads((ROOT/'ci/quarantine.json').read_text()),datetime.date(2026,10,29))
        names={test for pkg,test in q}
        self.assertEqual(names, {
            'TestReadPathGoldenEquivalence',
            'TestAlbumsJoinByReleaseAndOwnerCanSeparateAFolder',
            'TestDerivedWorkerCompletesWithForegroundPressure',
            'TestFacetRuntimeColdCompletesUnderContinuousTaggedIngest',
            'TestRealLocalAudioScanChaptersArtAndBookResume',
            'TestManifestPublicationWaitsForTheSessionGeneration',
        })
        self.assertNotIn('TestMBStagingTenThousandCompleteFinalWriterHold',names)
        self.assertNotIn('TestReadingInitialReadyHasNoPollDelayOrHiddenHandle',names)

class IntegrationTests(unittest.TestCase):
    def test_docs_graphics_do_not_fan_out_and_deletions_do(self):
        from unittest.mock import patch
        with patch('plan.Path.is_file',return_value=True):
            self.assertEqual(components(['docs/guide.md','web/logo.svg']),([],False))
        with patch('plan.Path.is_file',return_value=False):
            self.assertTrue(components(['missing.svg'])[1])
    def test_shard_builds_helper_locally(self):
        from unittest.mock import patch
        from run import go
        with patch('run.validate_tags'), patch('run.Path.is_file',return_value=False), patch('run.build_helper',side_effect=RuntimeError('local helper')) as build:
            with self.assertRaisesRegex(RuntimeError,'local helper'): go({'shards':[[]]},0)
            build.assert_called_once()
    def test_build_validates_and_covers_both_modules_and_tagsets(self):
        from unittest.mock import patch
        from run import build
        with patch('run.execute') as execute, patch('run.Path.mkdir'):
            build({'tags':'portico_rar7z,portico_cb7'})
        calls=[c.args for c in execute.call_args_list]
        builds=[c for c in calls if c[0][:3]==['go','build','-p=2']]
        vets=[c for c in calls if c[0][:1]==['sh'] and 'vet-go.sh' in c[0][1]]
        self.assertEqual(len(builds),4); self.assertEqual(len(vets),4)
        self.assertEqual({c[0][4] for c in builds},{'', 'portico_rar7z,portico_cb7'})

class MergeGroupTests(unittest.TestCase):
    def test_merge_group_uses_event_base_head(self):
        from unittest.mock import patch
        from plan import base_head
        with patch('plan.command',side_effect=['b'*40+'\n','','','']):
            self.assertEqual(base_head({'merge_group':{'base_sha':'a'*40,'head_sha':'b'*40}}, {'GITHUB_EVENT_NAME':'merge_group'}),('a'*40,'b'*40))

class TierTests(unittest.TestCase):
    def test_merge_tier_does_not_exclude_reviewed_deterministic_tests(self):
        from unittest.mock import patch
        from plan import make_plan
        with patch('plan.components',return_value=([],False)), patch('plan.command',return_value='portico_rar7z,portico_cb7'):
            self.assertEqual(len(make_plan([],tier='merge')['tier_exclusions']),5)
            self.assertEqual(len(make_plan([],tier='nightly')['tier_exclusions']),6)
            self.assertEqual(len(make_plan([],tier='short')['tier_exclusions']),5)
