"""Invocation caches must preserve the existing counters and fail closed."""
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch
from ratchet_runtime import Budget, GitBlobs, RatchetFailure, RatchetRuntime, TOOL_ROOT


class RatchetRuntimeTests(unittest.TestCase):
    def test_activation_cannot_spend_a_stale_high_direct_budget(self):
        from client_api_census import base_guard
        key='web/src/a.ts#'+'a'*16
        pin={'category':'platform-transport','reason':'Approved forwarding','sites':[key],'guardHashes':{key:'b'*64}}
        ledger={key:{'category':pin['category'],'reason':pin['reason'],'guardHash':'b'*64,
                     'condition':'Reviewed complete span','occurrences':1,
                     'rulingUrl':'https://github.com/PorticoMediaServer/portico/pull/880#issuecomment-6648755492'}}
        errors=base_guard({key:1},{'web/src/a.ts':pin},{key:99},{},ledger,site_counts={key:2},base_site_counts={key:1})
        self.assertTrue(any('live source occurrences' in error for error in errors),errors)
        self.assertFalse(base_guard({},{'web/src/a.ts':pin},{key:99},{},ledger,site_counts={key:1},base_site_counts={key:1}))

    def test_native_text_preserves_existing_universal_newline_semantics(self):
        from ratchet_runtime import source_text
        from client_api_census import native_sites
        source='fun relay(url: String) {\r\n val request = Request.Builder().url(url).get().build()\r\n}\r\n'
        self.assertEqual(native_sites('apps/native/Probe.kt',source_text(source.encode())),
                         native_sites('apps/native/Probe.kt',source.replace('\r\n','\n')))

    def test_dependency_changes_match_uncached_scanners(self):
        result=subprocess.run(['node',str(TOOL_ROOT/'ci/ratchet_runtime_fixture.mjs')],cwd=TOOL_ROOT,capture_output=True,text=True,timeout=45)
        self.assertEqual(result.returncode,0,result.stdout+'\n'+result.stderr)
        self.assertGreater(json.loads(result.stdout)['assertions'],40)

    def test_hard_budget_terminates_a_stuck_subprocess_and_rejects_invalid_limits(self):
        import sys
        with self.assertRaisesRegex(RatchetFailure,'budget exceeded'):
            Budget(.02).run([sys.executable,'-c','import time; time.sleep(10)'])
        for value in [0,-1,361,float('nan'),float('inf')]:
            with self.subTest(value=value),self.assertRaises(RatchetFailure):Budget(value)

    def test_worker_error_missing_output_and_timeout_fail_closed(self):
        from ratchet_runtime import TypedWorker, SourceView
        import sys
        for program,expected in [('pass','without a result'),('print("not json")','transport failed'),
                                 ('print(\'{"ok":false,"error":"injected"}\')','injected'),
                                 ('import time; time.sleep(10)','budget exceeded')]:
            with self.subTest(program=program),tempfile.TemporaryDirectory() as directory:
                real_popen=subprocess.Popen
                def fake(*args,**kwargs):return real_popen([sys.executable,'-c',program],**kwargs)
                with patch('ratchet_runtime.subprocess.Popen',side_effect=fake):
                    worker=TypedWorker(TOOL_ROOT,'census',Budget(.15),directory)
                try:
                    with self.assertRaisesRegex(RatchetFailure,expected):worker.scan(SourceView('empty',[],{},directory),[])
                finally:worker.close()

    def test_exact_blob_cache_is_shared_and_never_reads_an_archive(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory)
            def git(*args):return subprocess.check_output(['git',*args],cwd=root,text=True).strip()
            git('init','-q');git('config','user.name','Justin Ehler');git('config','user.email','justin@justinehler.com')
            path=root/'web/src/a.ts';path.parent.mkdir(parents=True);path.write_text("fetch('/v1/a');")
            git('add','.');git('commit','-qm','Source one');first=git('rev-parse','HEAD')
            (root/'unrelated.txt').write_text('Uncounted change');git('add','.');git('commit','-qm','Source two');second=git('rev-parse','HEAD')
            with RatchetRuntime(root) as runtime:
                a=runtime.historical(first);b=runtime.historical(second)
                self.assertEqual(a.records,b.records)
                self.assertEqual(runtime.git.stats['blobReads'],1)
                self.assertEqual(git('rev-parse','HEAD'),second)
                for view,expected in [(a,first),(b,second)]:
                    self.assertEqual(subprocess.check_output(['git','rev-parse','HEAD'],cwd=view.root,text=True).strip(),expected)
                    self.assertFalse((view.root/'node_modules').exists())


if __name__=='__main__':unittest.main()
