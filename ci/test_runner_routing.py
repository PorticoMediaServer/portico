import copy
import re
import unittest
from pathlib import Path
from plan import go_matrix


class RunnerRouting(unittest.TestCase):
    def test_go_matrix_preserves_every_shard_index_and_selection(self):
        for count in [0, 1, 10]:
            plan={'shards':[[{'name':f'Test{i}'}] for i in range(count)]}
            original=copy.deepcopy(plan)
            self.assertEqual(go_matrix(plan), {'include':[{'index':i} for i in range(count)]})
            self.assertEqual(plan, original)

    def test_merge_nightly_and_android_jobs_are_hosted(self):
        root=Path(__file__).resolve().parents[1]
        for name in ['merge.yml','nightly.yml','android-tests.yml']:
            workflow=(root/'.github/workflows'/name).read_text()
            selectors=re.findall(r'^    runs-on: (.+)$',workflow,re.M)
            self.assertTrue(selectors, name)
            # GitHub-hosted only. Nightly also runs the report-only macOS suite on hosted macOS.
            allowed={'ubuntu-24.04','macos-15'} if name=='nightly.yml' else {'ubuntu-24.04'}
            self.assertLessEqual(set(selectors), allowed, name)
            self.assertIn('ubuntu-24.04', set(selectors), name)
            self.assertNotIn('portico-vps',workflow)
            self.assertNotIn('self-hosted',workflow)
            self.assertNotIn('max-parallel:',workflow)

    def test_hosted_only_input_is_an_explicit_no_op(self):
        root=Path(__file__).resolve().parents[1]
        for name in ['merge.yml','nightly.yml']:
            workflow=(root/'.github/workflows'/name).read_text()
            self.assertIn('      hosted_only:',workflow)
            self.assertIn('description: No-op;',workflow)
            self.assertNotIn('inputs.hosted_only',workflow)
            self.assertNotIn('CI_HOSTED_ONLY',workflow)
        self.assertNotIn('vps_trusted',(root/'ci/plan.py').read_text())

    def test_no_public_workflow_requests_a_vps_label(self):
        folder=Path(__file__).resolve().parents[1]/'.github/workflows'
        for path in folder.glob('*.yml'):
            self.assertNotIn('portico-vps',path.read_text(),path.name)
