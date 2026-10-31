from pathlib import Path
import tempfile
import unittest
from explain import render_summary, selection_reasons
from js_affected import select
from plan import components

class Explain(unittest.TestCase):
    def test_reasons_describe_existing_component_edges(self):
        why=selection_reasons(['web/src/main.ts'],False,['web','packages'],False,components,{})
        self.assertEqual(why['components']['packages'][0]['reason'],'web -> packages dependency')
        why=selection_reasons(['ci/plan.py'],False,['web'],True,components,{})
        self.assertTrue(why['broad_reasons'])
        self.assertIn('full Go inventory',why['go'])
    def test_diagnostics_do_not_change_affected_selection(self):
        with tempfile.TemporaryDirectory() as d:
            root=Path(d);(root/'scripts').mkdir()
            (root/'scripts/ci-affected.py').write_text((Path(__file__).resolve().parents[1]/'scripts/ci-affected.py').read_text())
            files={'web/src/a.ts':'export const a=1;', 'web/test/a.test.ts':"import '../src/a.ts';",'web/test/unrelated.test.ts':''}
            for file,text in files.items():
                p=root/file;p.parent.mkdir(parents=True,exist_ok=True);p.write_text(text)
            candidates=[p for p in files if '.test.' in p]
            for paths in [['web/src/a.ts'],['web/deleted.ts'],['web/new-config.json']]:
                detail={};before=select(root,paths,candidates);after=select(root,paths,candidates,detail)
                self.assertEqual(after,before)
                self.assertTrue(detail.get('fallback') or detail.get('dependency_edges'))
    def test_summary_escapes_paths_and_marks_unmeasured_js(self):
        p={'paths':['<script>'], 'inventory':[], 'packages':[], 'tier_exclusions':[], 'shards':[],
           'predicted_seconds':[], 'js':[{'component':'web','files':['web/test/a.test.ts']}],
           'selection_diagnostics':{'components':{},'go':'none','js':{},'broad_reasons':[]}}
        summary=render_summary(p)
        self.assertIn('&lt;script&gt;',summary);self.assertIn('unmeasured',summary)
