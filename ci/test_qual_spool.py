"""Tiny streaming/compression controls, never database or qualification runs."""
import gzip
import hashlib
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import threading
import unittest
from unittest import mock

import qual_class as c
import qual_spool as s


class CompressedSpool(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)

    def collect(self, raw, limit=s.COMPRESSED_BYTES):
        a,b=socket.socketpair()
        thread=threading.Thread(target=s.capture,args=(b,self.root,limit))
        thread.start()
        try:
            a.sendall(raw)
        except BrokenPipeError:
            pass
        finally:
            a.close()
        thread.join(timeout=5)
        self.assertFalse(thread.is_alive())
        return json.loads((self.root/'capture.json').read_text())

    def test_stream_roundtrip_and_compressed_wire_verification(self):
        cfg=dict(run_id='control',host_epoch='control',source_commit='a'*40,profile='standard',**{'class':'inventory_n1'})
        base=dict(schema='portico-canonical-held-class-v1',run_id='control',host_epoch='control',declared_source_commit='a'*40,profile='standard',**{'class':'inventory_n1'})
        raw=b''.join(json.dumps(dict(base,sequence=n,kind=kind,complete=True,payload='abc'*10000)).encode()+b'\n' for n,kind in enumerate(['start','final'],1))
        result=self.collect(raw)
        self.assertTrue(result['complete']);self.assertTrue(result['verified_roundtrip'])
        self.assertEqual(gzip.decompress((self.root/'raw.ndjson.gz').read_bytes()),raw)
        self.assertEqual(result['original_sha256'],hashlib.sha256(raw).hexdigest())
        self.assertEqual(c.verify_stream(self.root/'raw.ndjson.gz',cfg,compressed=True)['raw_sha256'],result['original_sha256'])
        self.assertLess(result['peak_compressed_spool_bytes'],len(raw))

    def test_cap_failure_is_explicit_and_never_writes_past_limit(self):
        result=self.collect(os.urandom(65536),limit=128)
        self.assertFalse(result['complete']);self.assertIn('cap exceeded',result['error'])
        self.assertLessEqual((self.root/'raw.ndjson.gz').stat().st_size,128)

    def test_failed_roundtrip_and_inside_engine_are_nonpassing(self):
        with mock.patch.object(s,'decoded_digest',return_value=(0,'wrong')):
            result=self.collect(b'unchanged raw\n')
        self.assertFalse(result['complete']);self.assertIn('round-trip mismatch',result['error'])
        result.update(complete=True,verified_roundtrip=True,observer_cgroup='/engine')
        (self.root/'capture.json').write_text(json.dumps(result))
        with mock.patch.object(s,'cgroup',return_value='/engine'):
            with self.assertRaisesRegex(ValueError,'outside engine'):s.wait_capture(self.root)

    def test_real_runner_mount_and_external_cgroup(self):
        if os.environ.get('PORTICO_CLASS_SPOOL_PROTOCOL')!='1':
            self.skipTest('PR-only actual tmpfs/systemd protocol')
        root=Path(os.environ['RUNNER_TEMP'])/'qual-class-memory'
        output=root.parent/'qual-class-out';output.mkdir(exist_ok=True)
        c.memory_evidence(root,output)
        raw=b'{"synthetic_protocol_only":true,"native_run":false}\n'
        code='''import sys,socket
from pathlib import Path
sys.path.insert(0,sys.argv[1])
import qual_spool as s, qual_fixture as q
root=Path(sys.argv[2]);a=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM)
a.connect(str(root/'raw.sock'));a.sendall(bytes.fromhex(sys.argv[3]));a.close()
q.atomic_json(root/'engine-check.json',s.wait_capture(root))
'''
        def invoke():
            return subprocess.run(['sudo','systemd-run','--unit=portico-canonical-class','--wait','--pipe','--collect',
                                   '/usr/bin/python3','-c',code,str(Path(__file__).resolve().parent),str(root),raw.hex()],check=True,timeout=30).returncode
        self.assertEqual(s.observe(root,invoke),0)
        proof=json.loads((root/'engine-check.json').read_text())
        self.assertNotEqual(proof['engine_cgroup'],proof['observer_cgroup'])
        c.q.atomic_json(output/'engine-check.json',proof)
        (root/'engine-check.json').unlink()
        c.q.atomic_json(output/'receipt.json',{'status':'failed','synthetic_protocol_only':True})
        c.persist_memory(root,output)
        self.assertEqual(gzip.decompress((output/'raw.ndjson.gz').read_bytes()),raw)
        self.assertFalse(any(root.iterdir()))
