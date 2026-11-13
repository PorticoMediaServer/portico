import datetime
import unittest
from plan import ROOT, balance, closure, components, decode_stream
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
        self.assertTrue(components(['apikit/deleted/file.go'])[1])
        self.assertEqual(components(['unknown.config']),components([],True))
        self.assertIn('apps/native',components(['packages/client-core/src/auth.ts'])[0])
    def test_server_next_is_independent(self):
        self.assertEqual(components(['server-next/go.mod', 'server-next/internal/db/schema.sql']), ([], False))
    def test_stream(self): self.assertEqual(decode_stream('{}\n{}'),[{},{}])
    def test_aggregate_absent_cancelled_skipped(self):
        p={'packages':[],'js':[]}; n={k:{'result':v} for k,v in [('planner','success'),('hygiene','success'),('go_build','skipped'),('go_vet_cross','skipped'),('go_tests','skipped'),('decoder_sandbox','skipped'),('js','skipped'),('js_static','skipped'),('android','skipped')]}
        import tempfile
        from pathlib import Path
        from unittest.mock import patch
        with tempfile.TemporaryDirectory() as directory, patch('run.ROOT',Path(directory)):
            aggregate(p,n)
            for enabled in (False, True):
                selected = dict(p, server_next=enabled)
                results = dict(n, server_next={'result': 'success' if enabled else 'skipped'})
                aggregate(selected, results)
                with self.assertRaises(ValueError):
                    aggregate(selected, dict(results, server_next={'result': 'failure'}))
            for state in ['cancelled','failure',None,'success']:
                bad=dict(n); bad['go_tests']={'result':state}
                with self.assertRaises(ValueError): aggregate(p,bad)
    def test_exact_quarantine_no_infra_waiver(self):
        events=[{'Test':'TestKnown/child','Action':'fail'},{'Test':'TestKnown','Action':'fail'},{'Action':'fail'}]
        self.assertEqual(classify(events,['TestKnown'],1,{'TestKnown'}),'quarantined')
        for extra,status in [({'Test':'TestOther','Action':'fail'},'test_failed'), ({'Output':'panic: oops'},'panic_or_race'), ({'Output':'panic: test timed out after 10m'},'timeout')]:
            self.assertEqual(classify(events+[extra],['TestKnown'],1,{'TestKnown'}),status)
        self.assertEqual(classify(events,['TestMissing'],1,{'TestKnown'}),'incomplete_events')
        self.assertEqual(classify(events,['TestKnown'],2,{'TestKnown'}),'infrastructure')
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
        with self.assertRaises(ValueError): js({'js':[{'component':'web','files':[],'shards':[],'static_roots':[]}]},'web')

class IntegrationTests(unittest.TestCase):
    def test_docs_graphics_do_not_fan_out_and_deletions_do(self):
        from unittest.mock import patch
        with patch('plan.Path.is_file',return_value=True):
            self.assertEqual(components(['docs/guide.md','web/logo.svg']),([],False))
        with patch('plan.Path.is_file',return_value=False):
            self.assertTrue(components(['missing.svg'])[1])
    def test_build_validates_and_covers_apikit_and_both_tagsets(self):
        from unittest.mock import patch
        from run import build
        with patch('run.execute') as execute, patch('run.Path.mkdir'):
            build({'tags':'portico_rar7z,portico_cb7'})
        calls=[c.args for c in execute.call_args_list]
        builds=[c for c in calls if c[0][:3]==['go','build','-p=2']]
        vets=[c for c in calls if c[0][:1]==['sh'] and 'vet-go.sh' in c[0][1]]
        self.assertEqual(len(builds),2); self.assertEqual(len(vets),2)
        self.assertEqual({c[0][4] for c in builds},{'', 'portico_rar7z,portico_cb7'})
        generator=next(i for i,c in enumerate(calls) if c[0]==['node',str(ROOT/'server-next/receiver/generate-cast-text.mjs'),'--check'])
        drift=next(i for i,c in enumerate(calls) if c[0]==['git','diff','--exit-code'])
        self.assertLess(generator,drift)

class MergeGroupTests(unittest.TestCase):
    def test_merge_group_plans_against_the_target_branch_tip(self):
        from unittest.mock import patch
        from plan import base_head
        group={'base_sha':'a'*40,'head_sha':'b'*40,'base_ref':'refs/heads/main'}
        with patch('plan.command',side_effect=['b'*40+'\n','','','','c'*40+'\n','d'*40+'\n']) as call:
            self.assertEqual(base_head({'merge_group':group}, {'GITHUB_EVENT_NAME':'merge_group'}),('d'*40,'b'*40))
        self.assertEqual(call.call_args_list[-2].args[0][:3],['git','rev-parse','--verify'])
        self.assertEqual(call.call_args.args[0],['git','merge-base','b'*40,'c'*40])
