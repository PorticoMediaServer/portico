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
        self.assertEqual([r['runner'] for r in rows],['portico-vps']*5+['ubuntu-24.04']*5)

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
