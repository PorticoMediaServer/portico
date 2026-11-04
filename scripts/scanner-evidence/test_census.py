import json,os,pathlib,subprocess,sys,tempfile,unittest
import census
class Census(unittest.TestCase):
    def test_partition_excludes_inflight_quantum_from_wait(self):
        names=['quantum','analysis','probe','accept','finish','scheduler_wait','pacing','title_wait','writer_begin','sql']
        r={'names':names,'windowNanos':10_000_000_000,'masks':{'32':4_000_000_000,'33':3_000_000_000,'37':3_000_000_000}}
        self.assertEqual(census.partition(r),{'scheduler_wait_without_quantum':4.,'other_quantum':3.,'probe_call':3.})
        r['windowNanos']+=1
        with self.assertRaises(AssertionError):census.partition(r)
    def test_process_trace_counts_short_native_child(self):
        with tempfile.TemporaryDirectory() as temp:
            root=pathlib.Path(temp);exe=root/'ffprobe';exe.symlink_to('/bin/true');trace=root/'private.trace'
            subprocess.run(['strace','-f','-ttt','-T','-e','trace=process','-o',str(trace),'--',sys.executable,'-c','import subprocess,sys; subprocess.run([sys.argv[1]],check=True)',str(exe)],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
            got=census.native_census(trace,0,1e20)
            self.assertEqual(got['programs']['ffprobe']['starts'],1)
            self.assertGreater(got['nativeLifetimeUnionSeconds'],0)
            self.assertGreater(got['nativeExecSyscallUnionSeconds'],0)
            self.assertNotIn(str(exe),json.dumps(got))
    @unittest.skipUnless(os.environ.get('GITHUB_ACTIONS')=='true','CI namespace policy control')
    def test_tracer_preserves_bwrap_namespace_boundary(self):
        with tempfile.TemporaryDirectory() as temp:
            trace=pathlib.Path(temp)/'private.trace'
            subprocess.run(['strace','-f','-ttt','-T','-e','trace=process','-o',str(trace),'--','bwrap','--unshare-user','--unshare-pid','--ro-bind','/','/','--proc','/proc','--dev','/dev','--die-with-parent','/bin/true'],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
            self.assertEqual(census.native_census(trace,0,1e20)['programs']['bwrap']['starts'],1)
    def test_supervisor_is_never_used_as_worker(self):
        with subprocess.Popen([sys.executable,'-c','import time;time.sleep(10)'],env={**os.environ,'PORTICO_SUPERVISED_WORKER':'1'}) as child:
            try:
                got=census.worker_identity(os.getpid(),child.pid,sys.executable)
                self.assertEqual(got['pid'],child.pid)
                self.assertGreater(census.tree_cpu(child.pid)['liveProcesses'],0)
                with self.assertRaises(AssertionError):census.worker_identity(child.pid,child.pid,sys.executable)
            finally:child.terminate();child.wait()
if __name__=='__main__':unittest.main()
