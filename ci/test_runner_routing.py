import unittest
from plan import go_matrix, vps_trusted

class RunnerRouting(unittest.TestCase):
    def test_only_declared_trusted_events_use_the_fixed_vps_share(self):
        env={'GITHUB_REPOSITORY':'PorticoMediaServer/portico'}
        for event in ['merge_group','schedule','workflow_dispatch']:
            self.assertTrue(vps_trusted({},dict(env,GITHUB_EVENT_NAME=event)))
        self.assertTrue(vps_trusted({},dict(env,GITHUB_EVENT_NAME='push',GITHUB_REF='refs/heads/main')))
        for event in ['', 'pull_request_target','issue_comment','push','repository_dispatch']:
            self.assertFalse(vps_trusted({},dict(env,GITHUB_EVENT_NAME=event)))
        plan={'shards':[[]]*10}
        rows=go_matrix(plan,{},dict(env,GITHUB_EVENT_NAME='merge_group'))['include']
        self.assertEqual([r['index'] for r in rows],list(range(10)))
        self.assertEqual([r['runner'] for r in rows],['portico-vps-go']+['ubuntu-24.04']*9)

    def test_forks_deleted_head_and_wrong_repository_fail_closed(self):
        env={'GITHUB_REPOSITORY':'PorticoMediaServer/portico','GITHUB_EVENT_NAME':'pull_request'}
        own={'pull_request':{'head':{'repo':{'full_name':'PorticoMediaServer/portico'}}}}
        self.assertTrue(vps_trusted(own,env))
        for repo in ['attacker/portico','PorticoMediaServer/another','']:
            event={'pull_request':{'head':{'repo':{'full_name':repo}}}}
            rows=go_matrix({'shards':[[],[]]},event,env)['include']
            self.assertEqual([r['runner'] for r in rows],['ubuntu-24.04']*2)
        self.assertFalse(vps_trusted({},env))
        self.assertFalse(vps_trusted({'pull_request':{'head':{'repo':None}}},env))
        self.assertFalse(vps_trusted(own,dict(env,GITHUB_REPOSITORY='attacker/portico')))

    def test_hosted_only_overrides_trusted_events_without_changing_shards(self):
        plan={'shards':[[{'name':'TestA'}], [{'name':'TestB'}]]}
        env={'GITHUB_REPOSITORY':'PorticoMediaServer/portico'}
        for name in ['merge_group','schedule','workflow_dispatch','push','pull_request']:
            event={'pull_request':{'head':{'repo':{'full_name':'PorticoMediaServer/portico'}}}}
            current=dict(env,GITHUB_EVENT_NAME=name,GITHUB_REF='refs/heads/main')
            self.assertTrue(vps_trusted(event,current))
            for value in ['true','TRUE']:
                rows=go_matrix(plan,event,dict(current,CI_HOSTED_ONLY=value))['include']
                self.assertEqual(rows,[{'index':0,'runner':'ubuntu-24.04'},{'index':1,'runner':'ubuntu-24.04'}])
            for value in ['', 'false']:
                rows=go_matrix(plan,event,dict(current,CI_HOSTED_ONLY=value))['include']
                self.assertEqual(rows[0]['runner'],'portico-vps-go')
        self.assertEqual([t[0]['name'] for t in plan['shards']],['TestA','TestB'])

    def test_override_is_wired_to_every_workflow_vps_selector(self):
        import re
        from pathlib import Path
        root=Path(__file__).resolve().parents[1]
        for name in ['merge.yml','nightly.yml']:
            workflow=(root/'.github/workflows'/name).read_text()
            self.assertRegex(workflow,r'      hosted_only:\n        description: [^\n]+\n        type: boolean\n        default: false')
            self.assertIn('CI_HOSTED_ONLY: ${{ inputs.hosted_only }}',workflow)
            for selector in re.findall(r'^    runs-on: (.+)$',workflow,re.M):
                if 'portico-vps' in selector:
                    self.assertIn('!inputs.hosted_only && github.repository',selector)
                else:
                    self.assertIn(selector,['ubuntu-24.04','${{ matrix.runner }}'])
            self.assertNotIn('max-parallel:',workflow)
        android=(root/'.github/workflows/android-tests.yml').read_text()
        self.assertEqual(set(re.findall(r'^    runs-on: (.+)$',android,re.M)),{'ubuntu-24.04'})
