"""Synthetic transport controls only; no database or qualification samples."""
import gzip
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock

import qual_class as c
import qual_population as population


class ClassProtocol(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='qual-class-control-', dir=Path.cwd())
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.cfg = dict(run_id='synthetic', host_epoch='synthetic', source_commit='a'*40,
                        profile='standard', **{'class': 'inventory_n1'})

    def records(self, records):
        path = self.root / 'raw.ndjson'
        with path.open('w') as f:
            for i, record in enumerate(records, 1):
                row = dict(schema='portico-canonical-held-class-v1', sequence=i,
                           run_id=self.cfg['run_id'], host_epoch=self.cfg['host_epoch'],
                           declared_source_commit=self.cfg['source_commit'],
                           profile=self.cfg['profile'], **{'class': self.cfg['class']})
                row.update(record)
                f.write(json.dumps(row)+'\n')
        return path

    def test_closed_inputs_and_real_long_run_ids(self):
        env = dict(INPUT_SOURCE_COMMIT='a'*40, INPUT_MANIFEST_SHA256='b'*64,
                   INPUT_CLASS='inventory_n1', PROFILE='legacy8', REPLICATE='3',
                   INPUT_PRODUCER_RUN='42388438790', GITHUB_RUN_ID='42388438791', GITHUB_RUN_ATTEMPT='1')
        cfg = c.inputs(env)
        self.assertEqual(cfg['producer_run'], 42388438790)
        for key, value in [('INPUT_SOURCE_COMMIT', 'main'), ('INPUT_CLASS', 'inventory_n1;id'),
                           ('PROFILE', 'reference'), ('REPLICATE', '4'), ('INPUT_PRODUCER_RUN', '1/../../')]:
            with self.subTest(key=key), mock.patch.object(c.subprocess, 'Popen') as popen:
                with self.assertRaises(ValueError): c.inputs(dict(env, **{key: value}))
                popen.assert_not_called()

    def test_checkpoint_interval_is_a_screening_only_owner_class_variable(self):
        env = dict(INPUT_SOURCE_COMMIT='a'*40, INPUT_MANIFEST_SHA256='b'*64, INPUT_CLASS='security', PROFILE='standard',
                   REPLICATE='1', INPUT_PRODUCER_RUN='1', GITHUB_RUN_ID='2', GITHUB_RUN_ATTEMPT='1')
        self.assertIsNone(c.inputs(env)['checkpoint_interval_ms'])
        self.assertEqual(c.inputs(dict(env, INPUT_PURPOSE='screening', INPUT_CHECKPOINT_INTERVAL_MS='150'))['checkpoint_interval_ms'], 150)
        for bad in [dict(INPUT_CHECKPOINT_INTERVAL_MS='150'),  # qualification refuses it
                    dict(INPUT_PURPOSE='screening', INPUT_CHECKPOINT_INTERVAL_MS='49'),
                    dict(INPUT_PURPOSE='screening', INPUT_CHECKPOINT_INTERVAL_MS='5001'),
                    dict(INPUT_PURPOSE='screening', INPUT_CHECKPOINT_INTERVAL_MS='1e2'),
                    dict(INPUT_PURPOSE='screening', INPUT_CHECKPOINT_INTERVAL_MS='150', INPUT_CLASS='checkpoint_1mib')]:
            with self.subTest(bad=bad), self.assertRaises(ValueError):
                c.inputs(dict(env, **bad))

    def test_offered_rate_is_a_wc_class_input_allowed_for_qualification(self):
        env = dict(INPUT_SOURCE_COMMIT='a'*40, INPUT_MANIFEST_SHA256='b'*64, INPUT_CLASS='security', PROFILE='standard',
                   REPLICATE='1', INPUT_PRODUCER_RUN='1', GITHUB_RUN_ID='2', GITHUB_RUN_ATTEMPT='1')
        self.assertIsNone(c.inputs(env)['offered_rate_per_sec'])
        self.assertEqual(c.inputs(dict(env, INPUT_OFFERED_RATE_PER_SEC='20'))['offered_rate_per_sec'], 20.0)
        self.assertEqual(c.inputs(dict(env, INPUT_OFFERED_RATE_PER_SEC='0.5'))['offered_rate_per_sec'], 0.5)
        for bad in ['0', '0.05', '1001', '1e2', '-3', '20/s', ' 20']:
            with self.subTest(bad=bad), self.assertRaises(ValueError):
                c.inputs(dict(env, INPUT_OFFERED_RATE_PER_SEC=bad))
        with self.assertRaises(ValueError):  # WC classes only
            c.inputs(dict(env, INPUT_CLASS='inventory_n1', INPUT_OFFERED_RATE_PER_SEC='20'))

    def test_checkpoint_screening_purpose_and_complete_native_census(self):
        env=dict(INPUT_SOURCE_COMMIT='a'*40,INPUT_MANIFEST_SHA256='b'*64,INPUT_CLASS='checkpoint_1mib',PROFILE='standard',REPLICATE='1',INPUT_PRODUCER_RUN='1',GITHUB_RUN_ID='2',GITHUB_RUN_ATTEMPT='1')
        with self.assertRaises(ValueError):c.inputs(env)
        self.assertEqual(c.inputs(dict(env,INPUT_PURPOSE='screening'))['purpose'],'screening')
        record=dict(Cell='1MiB',Kind='concurrent',CheckpointError='',Checkpoint=dict(Refusal=None,PhysicalSafe=True),CheckpointHoldNS=42)
        raw=self.root/'cp.gz'
        def write(n):raw.write_bytes(gzip.compress(b''.join(b'    owner.go:42: GV_KEEPER_CONCURRENT_V1 '+json.dumps(record).encode()+b'\n' for _ in range(n))))
        with mock.patch.object(c,'ATTEMPTS',2):
            write(2);self.assertEqual(c.verify_checkpoint_stream(raw,'1MiB',2)['records'],2)
            write(1)
            with self.assertRaises(ValueError):c.verify_checkpoint_stream(raw,'1MiB',2)
            record['CheckpointError']='retained native error';write(2)
            with self.assertRaises(ValueError):c.verify_checkpoint_stream(raw,'1MiB',2)

    def test_owner_completion_is_not_qualification(self):
        path = self.records([{'kind': 'start'}, {'kind': 'final', 'complete': True}])
        result = c.verify_stream(path, self.cfg)
        self.assertIsNone(result['qualification_verdict'])
        self.assertEqual(result['records'], 2)
        self.assertNotIn('p99', result)

    def test_complete_stream_can_exceed_old_4gib_transport_ceiling(self):
        # Simulate the filesystem length, not a native/large local population.
        # The actual tiny wire is still parsed and hashed by the real verifier.
        path = self.records([{'kind':'start'}, {'kind':'final', 'complete':True}])
        actual_stat = Path.lstat
        def reported_size(p, *args, **kwargs):
            value = actual_stat(p, *args, **kwargs)
            if p == path:
                fields = list(value); fields[6] = self.reported_stream_size
                return os.stat_result(fields)
            return value
        with mock.patch.object(Path, 'lstat', reported_size):
            self.reported_stream_size = 7 << 30
            self.assertEqual(c.verify_stream(path, self.cfg)['records'], 2)
            self.reported_stream_size = (16 << 30) + 1
            with self.assertRaises(ValueError): c.verify_stream(path, self.cfg)

    def test_failed_binding_and_incomplete_final_cannot_pass(self):
        for records in [[{'kind': 'start'}, {'kind': 'failed_binding'}, {'kind': 'final', 'complete': True}],
                        [{'kind': 'start'}, {'kind': 'final', 'complete': False}],
                        [{'kind': 'start'}], [{'kind': 'final', 'complete': True}]]:
            with self.subTest(records=records), self.assertRaises(ValueError):
                c.verify_stream(self.records(records), self.cfg)

    def test_sequence_pin_and_post_final_drift_fail(self):
        for record in [{'kind': 'final', 'complete': True, 'sequence': 4},
                       {'kind': 'final', 'complete': True, 'declared_source_commit': 'c'*40},
                       {'kind': 'final', 'complete': True, 'profile': 'legacy8'}]:
            with self.subTest(record=record), self.assertRaises(ValueError):
                c.verify_stream(self.records([{'kind': 'start'}, record]), self.cfg)
        with self.assertRaises(ValueError):
            c.verify_stream(self.records([{'kind': 'start'}, {'kind': 'final', 'complete': True}, {'kind': 'extra'}]), self.cfg)

    def test_truncated_or_duplicate_record_fails(self):
        path = self.records([{'kind': 'start'}, {'kind': 'final', 'complete': True}])
        path.write_bytes(path.read_bytes().rstrip(b'\n'))
        with self.assertRaises(ValueError): c.verify_stream(path, self.cfg)
        path.write_text('{"schema":"portico-canonical-held-class-v1","sequence":1,"sequence":2}\n')
        with self.assertRaises(ValueError): c.verify_stream(path, self.cfg)

    def test_profile_properties_keep_caps_separate(self):
        self.assertIn('MemoryMax=8589934592', c.profile_properties('legacy8'))
        self.assertIn('MemorySwapMax=0', c.profile_properties('legacy8'))
        self.assertIn('CPUQuota=200%', c.profile_properties('legacy8'))
        self.assertIn('MemoryMax=infinity', c.profile_properties('standard'))
        self.assertIn('CPUQuota=400%', c.profile_properties('standard'))
        with self.assertRaises(ValueError): c.profile_properties('caller-cap')

    def test_successful_producer_attempt_required(self):
        cfg = dict(producer_run=42388438790, producer_attempt=1, manifest='b'*64)
        receipt = dict(status='complete', inputs={'purpose':'qualification'}, run_id='42388438790',
                       run_attempt='1', manifest_sha256='b'*64, tooling_commit='a'*40)
        run = dict(id=42388438790, run_attempt=1, status='completed', conclusion='success',
                   path='.github/workflows/store-qualification-fixture.yml', head_branch='main',
                   event='workflow_dispatch', head_sha='a'*40)
        c.verify_producer(receipt, run, cfg)
        for key, value in [('conclusion','failure'), ('run_attempt',2), ('head_branch','lane/preview')]:
            with self.subTest(key=key), self.assertRaises(ValueError):
                c.verify_producer(receipt, dict(run, **{key:value}), cfg)
        with self.assertRaises(ValueError): c.verify_producer(dict(receipt, inputs={'purpose':'preview'}), run, cfg)

    def test_missing_cells_never_become_a_summary_pass(self):
        artifacts, output = self.root/'artifacts', self.root/'summary'
        artifacts.mkdir()
        with self.assertRaises(ValueError): c.summarize(artifacts, output)
        summary = json.loads((output/'summary.json').read_text())
        self.assertIsNone(summary['qualification_verdict'])
        self.assertTrue(summary['protocol_errors'])

    def test_supervisor_retains_failed_exit_and_requires_process_drain(self):
        # No privileges, binaries, cgroups or host evidence are actually used.
        original_read = Path.read_text
        boot_path = '/proc/sys/kernel/random/boot_id'
        def read(path, *args, **kwargs):
            if str(path) == boot_path: return '11111111-1111-1111-1111-111111111111'
            return original_read(path, *args, **kwargs)
        for ordinal, (exit_code, leftovers) in enumerate(((0, False), (1, False), (0, True))):
            root = self.root/str(ordinal)
            root.mkdir(); work=root/'work'; work.mkdir(); output=root/'out'; output.mkdir()
            for name in ('writer.test', 'host'): (work/name).write_bytes(b'synthetic control only')
            cfg = dict(source='a'*40, name='inventory_n1', profile='standard', replicate=1,
                       workflow_run=1, workflow_attempt=1, manifest='b'*64)
            launch = dict(cfg=cfg, source=str(root/'source'), artifact=str(root/'population'),
                          output=str(output), work=str(work), env={}, uid=1001, gid=1001,
                          binary_sha256=c.q.file_hash(work/'writer.test'), host_sha256=c.q.file_hash(work/'host'), archive_sha256='c'*64)
            c.q.atomic_json(work/'launch.json', launch)
            c.q.atomic_json(output/'receipt.json', {'status':'prepared-only'})
            def host(argv, cwd, env, report, name, timeout):
                path=report/(name+'.log'); c.q.atomic_json(path, {'synthetic':'not real host evidence'}); return path
            def waited(timeout):
                self.cfg=json.loads((work/'config.json').read_text())
                raw=self.records([{'kind':'start'}, {'kind':'final', 'complete':True}])
                target=Path(self.cfg['output_path']).parent/'raw.ndjson.gz'; target.parent.mkdir(parents=True, exist_ok=True); target.write_bytes(gzip.compress(raw.read_bytes()))
                return exit_code
            process=mock.Mock(pid=12345); process.wait.side_effect=waited
            with mock.patch.object(c.os, 'geteuid', return_value=0), mock.patch.object(Path, 'read_text', read), \
                 mock.patch.object(c.q, 'command', side_effect=host), \
                 mock.patch.object(c.subprocess, 'Popen', return_value=process) as popen, \
                 mock.patch.object(c, 'memory_evidence', return_value={'synthetic':'not actual tmpfs evidence'}), \
                 mock.patch.object(c.spool, 'wait_capture', return_value={'synthetic':True}), \
                 mock.patch.object(c.spool, 'engine_memory_peak', return_value=123), \
                 mock.patch.object(c, 'own_cgroup_members', return_value=[os.getpid(), 999999] if leftovers else [os.getpid()]):
                if exit_code or leftovers:
                    with self.assertRaises(ValueError): c.run(work/'launch.json')
                else: c.run(work/'launch.json')
                self.assertEqual(popen.call_args.kwargs['user'], 1001)
                self.assertEqual(popen.call_args.kwargs['group'], 1001)
                self.assertEqual(popen.call_args.kwargs['extra_groups'], [])
                self.assertNotIn('shell', popen.call_args.kwargs)
            receipt=json.loads((output/'receipt.json').read_text())
            self.assertEqual(receipt['status'], 'failed' if exit_code or leftovers else 'owner-execution-complete')
            self.assertEqual(receipt['target_exit_code'], exit_code)
            self.assertIn('host_after', receipt)
            self.assertIsNone(receipt['qualification_verdict'])

    def test_six_cells_keep_identity_and_reject_duplicate_vms(self):
        artifacts, output = self.root/'cells', self.root/'summary'
        artifacts.mkdir()
        for profile in ('standard', 'legacy8'):
            for replicate in (1, 2, 3):
                directory = artifacts/f'canonical-class-{profile}-vm{replicate}'
                directory.mkdir()
                receipt = dict(status='owner-execution-complete',
                    declared=dict(profile=profile, replicate=replicate, source='a'*40,
                                  name='inventory_n1', manifest='b'*64, producer_run=42388438790, producer_attempt=1),
                    host_before={'host':{'files':{'proc/sys/kernel/random/boot_id':f'synthetic-{profile}-{replicate}'}}})
                c.q.atomic_json(directory/'receipt.json', receipt)
                c.finalize(directory)
        c.summarize(artifacts, output)
        summary = json.loads((output/'summary.json').read_text())
        self.assertEqual(len(summary['cells']), 6)
        self.assertIsNone(summary['qualification_verdict'])
        # A new job label is not a new VM. Rehash to isolate that guard.
        directory = artifacts/'canonical-class-standard-vm3'
        receipt = json.loads((directory/'receipt.json').read_text())
        receipt['host_before']['host']['files']['proc/sys/kernel/random/boot_id'] = 'synthetic-standard-1'
        c.q.atomic_json(directory/'receipt.json', receipt)
        c.finalize(directory)
        with self.assertRaisesRegex(ValueError, 'duplicate VM'):
            c.summarize(artifacts, output)

    def test_cleanup_readonly_module_directories_and_symlinks(self):
        temporary, workspace = self.root/'runner-temp', self.root/'workspace'
        temporary.mkdir(); workspace.mkdir()
        module=temporary/'qual-class-work/gopath/pkg/mod/example@v1/nested'
        module.mkdir(parents=True)
        (module/'readonly.go').write_text('synthetic readonly module')
        (module/'readonly.go').chmod(0o444)
        module.chmod(0o555);module.parent.chmod(0o555)
        (workspace/'population').mkdir()
        (workspace/'population/population.sqlite').write_bytes(b'synthetic, not SQLite')
        external=self.root/'preserve';external.mkdir();(external/'keep').write_text('preserve')
        (temporary/'qual-class-work/link').symlink_to(external, target_is_directory=True)
        with mock.patch.dict(os.environ, RUNNER_TEMP=str(temporary), GITHUB_WORKSPACE=str(workspace)), \
             mock.patch.object(c, 'retired_unit', return_value={'LoadState':'not-found','ActiveState':'inactive'}):
            c.cleanup(keep_output=True)
            proof=json.loads((temporary/'qual-class-out/cleanup.json').read_text())
            self.assertEqual(proof['errors'], [])
            self.assertFalse((temporary/'qual-class-work').exists())
            self.assertFalse((workspace/'population').exists())
            self.assertEqual((external/'keep').read_text(), 'preserve')
            c.cleanup()
            self.assertFalse((temporary/'qual-class-out').exists())

    def test_retired_unit_requires_complete_stopped_and_drained_evidence(self):
        good='LoadState=not-found\nActiveState=inactive\nControlGroup=\n'
        for raw in (good, good.replace('not-found','loaded').replace('ControlGroup=', 'ControlGroup=/system.slice/portico-canonical-class.service')):
            with mock.patch.object(c.subprocess, 'run', return_value=mock.Mock(returncode=0, stdout=raw)), \
                 mock.patch.object(Path, 'read_text', return_value='populated 0\nfrozen 0\n'):
                self.assertEqual(c.retired_unit()['ActiveState'], 'inactive')
        for raw in (good.replace('inactive','active'), good.replace('ControlGroup=\n',''),
                    good.replace('ControlGroup=', 'ControlGroup=/'),
                    good+'ActiveState=inactive\n'):
            with self.subTest(raw=raw), mock.patch.object(c.subprocess, 'run', return_value=mock.Mock(returncode=0, stdout=raw)):
                with self.assertRaises(ValueError): c.retired_unit()
        with mock.patch.object(c.subprocess, 'run', return_value=mock.Mock(returncode=0, stdout=good.replace('ControlGroup=', 'ControlGroup=/system.slice/portico-canonical-class.service'))), \
             mock.patch.object(Path, 'read_text', return_value='populated 1\nfrozen 0\n'):
            with self.assertRaises(ValueError): c.retired_unit()

    def test_cleanup_refuses_unproven_process_retirement(self):
        temporary, workspace = self.root/'temp', self.root/'workspace'
        temporary.mkdir(); workspace.mkdir()
        work=temporary/'qual-class-work';work.mkdir();(work/'preserve').write_text('live work')
        with mock.patch.dict(os.environ, RUNNER_TEMP=str(temporary), GITHUB_WORKSPACE=str(workspace)), \
             mock.patch.object(c, 'retired_unit', side_effect=ValueError('active')):
            with self.assertRaises(ValueError): c.cleanup(keep_output=True)
        self.assertEqual((work/'preserve').read_text(),'live work')
        proof=json.loads((temporary/'qual-class-out/cleanup.json').read_text())
        self.assertTrue(proof['errors'])
        self.assertEqual(json.loads((temporary/'qual-class-out/receipt.json').read_text())['status'],'failed')

    def test_memory_spool_requires_real_bounded_distinct_tmpfs(self):
        root=self.root/'qual-class-memory'; root.mkdir(); database=self.root/'database'; database.mkdir()
        actual_stat=Path.stat
        def stat(path,*args,**kwargs):
            result=actual_stat(path,*args,**kwargs)
            if path==root:
                fields=list(result);fields[2]+=1;return os.stat_result(fields)
            return result
        mount={'filesystems':[{'target':str(root),'fstype':'tmpfs','source':'portico-class-raw','options':'rw,noswap,nosuid,nodev,noexec'}]}
        result=mock.Mock(stdout=json.dumps(mount))
        with mock.patch.object(c.subprocess,'run',return_value=result), \
             mock.patch.object(Path,'stat',stat), \
             mock.patch.object(c.os,'statvfs',return_value=SimpleNamespace(f_blocks=c.RAW_MEMORY_BYTES//4096,f_frsize=4096)):
            self.assertEqual(c.memory_evidence(root,database)['limit_bytes'],768<<20)
            mount['filesystems'][0]['fstype']='ext4';result.stdout=json.dumps(mount)
            with self.assertRaises(ValueError): c.memory_evidence(root,database)
            mount['filesystems'][0]['fstype']='tmpfs';mount['filesystems'][0]['options']='rw,nosuid,nodev,noexec';result.stdout=json.dumps(mount)
            with self.assertRaises(ValueError): c.memory_evidence(root,database)
            mount['filesystems'][0]['options']='rw,noswap,nosuid,nodev,noexec';result.stdout=json.dumps(mount)
            with mock.patch.object(c.os,'statvfs',return_value=SimpleNamespace(f_blocks=1,f_frsize=4096)):
                with self.assertRaises(ValueError): c.memory_evidence(root,database)
        with mock.patch.object(c.subprocess,'run',return_value=result), \
             mock.patch.object(c.os,'statvfs',return_value=SimpleNamespace(f_blocks=c.RAW_MEMORY_BYTES//4096,f_frsize=4096)):
            with self.assertRaises(ValueError): c.memory_evidence(root,database)

    def test_memory_persist_preserves_exact_gzip_after_join_and_failure(self):
        for complete in (False,True):
            root=self.root/str(complete)/'qual-class-memory';root.mkdir(parents=True)
            output=root.parent/'out';output.mkdir();original=b'{"sequence":1,"unchanged":"raw bytes"}\n'
            packed=gzip.compress(original);(root/'raw.ndjson.gz').write_bytes(packed)
            c.q.atomic_json(root/'capture.json',dict(complete=complete,stored_bytes=len(packed),stored_sha256=c.hashlib.sha256(packed).hexdigest(),original_bytes=len(original),original_sha256=c.hashlib.sha256(original).hexdigest(),error='explicit observer failure'))
            c.q.atomic_json(output/'receipt.json',{'status':'failed'})
            with mock.patch.object(c,'retired_unit',return_value={'ActiveState':'inactive'}), \
                 mock.patch.object(c,'memory_evidence',return_value={'synthetic':True}):
                if complete: c.persist_memory(root,output)
                else:
                    with self.assertRaises(ValueError): c.persist_memory(root,output)
                self.assertEqual((output/'raw.ndjson.gz').read_bytes(),packed)
                self.assertFalse(any(root.iterdir()))
            self.assertEqual(json.loads((output/'receipt.json').read_text())['status'],'failed')
        with mock.patch.object(c,'retired_unit',side_effect=ValueError('still running')), \
             mock.patch.object(c.shutil,'copyfile') as copy:
            with self.assertRaises(ValueError): c.persist_memory(root,output)
            copy.assert_not_called()

    def test_missing_capture_preserves_compressed_prefix_as_failed(self):
        root=self.root/'qual-class-memory';root.mkdir();output=self.root/'out'
        packed=b'partial gzip prefix';(root/'raw.ndjson.gz').write_bytes(packed)
        with mock.patch.object(c,'retired_unit',return_value={}), mock.patch.object(c,'memory_evidence',return_value={}):
            with self.assertRaises(ValueError):c.persist_memory(root,output)
        self.assertEqual((output/'raw.ndjson.gz').read_bytes(),packed)
        self.assertEqual(json.loads((output/'receipt.json').read_text())['status'],'failed')
        self.assertFalse(any(root.iterdir()))

    def test_memory_unmount_refuses_unpersisted_raw(self):
        root=self.root/'qual-class-memory';root.mkdir();(root/'raw.ndjson').write_bytes(b'preserve original')
        c.q.atomic_json(root.parent/'qual-class-memory-owner.json',{'path':str(root)})
        with mock.patch.object(c,'retired_unit',return_value={}), mock.patch.object(c.os,'geteuid',return_value=0), \
             mock.patch.object(c.os.path,'ismount',return_value=True), mock.patch.object(c,'memory_evidence',return_value={}), \
             mock.patch.object(c.subprocess,'run') as command:
            with self.assertRaises(ValueError):c.memory_cleanup(root,self.root/'out')
            command.assert_not_called()
        self.assertEqual((root/'raw.ndjson').read_bytes(),b'preserve original')

    def test_finalize_roundtrips_compressed_raw_without_changing_verification(self):
        for status in ('failed', 'owner-execution-complete'):
            with self.subTest(status=status):
                output = self.root/status; output.mkdir()
                original = b'original synthetic prefix\n' * 1000
                raw=output/'raw.ndjson'; raw.write_bytes(original)
                receipt={'status':status}
                if status == 'owner-execution-complete':
                    receipt['wire']={'raw_sha256':c.q.file_hash(raw)}
                c.q.atomic_json(output/'receipt.json', receipt)
                c.finalize(output)
                self.assertFalse(raw.exists())
                self.assertEqual(gzip.decompress((output/'raw.ndjson.gz').read_bytes()), original)
                storage = json.loads((output/'raw-storage.json').read_text())
                self.assertTrue(storage['verified_roundtrip'])
                self.assertEqual(storage['wire_verified'], status == 'owner-execution-complete')
                self.assertEqual(storage['original_bytes'], len(original))
                self.assertEqual(json.loads((output/'receipt.json').read_text())['status'], status)
                inventory = json.loads((output/'files.json').read_text())
                self.assertIn('raw.ndjson.gz', {v['path'] for v in inventory})
                self.assertNotIn('raw.ndjson', {v['path'] for v in inventory})

    def test_finalize_preserves_original_on_verified_digest_mismatch(self):
        output = self.root/'mismatch'; output.mkdir()
        original = b'changed after wire verification\n'
        (output/'raw.ndjson').write_bytes(original)
        c.q.atomic_json(output/'receipt.json', {'status':'owner-execution-complete','wire':{'raw_sha256':'0'*64}})
        with self.assertRaises(ValueError): c.finalize(output)
        self.assertEqual((output/'raw.ndjson').read_bytes(), original)
        self.assertFalse((output/'raw-storage.json').exists())
        self.assertEqual(json.loads((output/'receipt.json').read_text())['status'], 'failed')

    def test_finalize_retains_failure_and_file_hashes(self):
        output = self.root/'partial'
        output.mkdir()
        (output/'raw.ndjson').write_text('partial raw prefix\n')
        c.finalize(output)
        self.assertEqual(json.loads((output/'receipt.json').read_text())['status'], 'failed')
        files = json.loads((output/'files.json').read_text())
        self.assertEqual({f['path'] for f in files}, {'raw.ndjson.gz', 'raw-storage.json', 'receipt.json'})


class OwnerTargetTransport(unittest.TestCase):
    """The owner's declaration is copied, never computed, by the transport."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='qual-class-target-', dir=Path.cwd())
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.receipt = dict(declared=dict(name='inventory_n1', profile='standard', replicate=1),
                            wire=dict(owner_final=dict(kind='final', complete=True)))
        self.declaration = dict(version='portico-class-target-v1', **{'class': 'inventory_n1'},
                                profile='standard', replicate=1, target='per-host p99 U95 <= 10 ms',
                                target_flag=True, reason='owner measured its own target', gate='hosted_p99_u95_10ms',
                                min_holds_per_host=10000, synthetic=False)

    def declare(self, declaration, receipt=None):
        receipt = self.receipt if receipt is None else receipt
        receipt['wire']['owner_final']['target'] = declaration
        return c.write_owner_target(self.root, receipt)

    def test_valid_declaration_is_written_exactly_with_receipt_identity(self):
        self.assertEqual(self.declare(self.declaration), self.declaration)
        self.assertEqual(json.loads((self.root/'target.json').read_text()), self.declaration)

    def test_absent_declaration_writes_nothing(self):
        self.assertIsNone(c.write_owner_target(self.root, self.receipt))
        self.assertFalse((self.root/'target.json').exists())
        self.receipt['wire']['owner_final']['target'] = None
        self.assertIsNone(c.write_owner_target(self.root, self.receipt))
        self.assertFalse((self.root/'target.json').exists())

    def test_null_flag_is_preserved(self):
        self.declare(dict(self.declaration, target_flag=None))
        written = json.loads((self.root/'target.json').read_text())
        self.assertIsNone(written['target_flag'])

    def test_unknown_field_is_refused(self):
        with self.assertRaisesRegex(ValueError, 'unknown owner target field'):
            self.declare(dict(self.declaration, extra='not part of the contract'))
        self.assertFalse((self.root/'target.json').exists())

    def test_disagreeing_identity_is_refused(self):
        for key, value in (('class', 'security'), ('profile', 'legacy8'), ('replicate', 2)):
            with self.subTest(key=key):
                self.root = self.root/key
                self.root.mkdir()
                with self.assertRaisesRegex(ValueError, key + ' disagrees'):
                    self.declare(dict(self.declaration, **{key: value}))
                self.assertFalse((self.root/'target.json').exists())

    def test_non_boolean_flag_is_refused(self):
        for flag in (1, 0, 'true', [], {}):
            with self.subTest(flag=flag), self.assertRaisesRegex(ValueError, 'target_flag'):
                self.declare(dict(self.declaration, target_flag=flag))
        self.assertFalse((self.root/'target.json').exists())

    def test_oversized_string_is_refused(self):
        for key, limit in c.TARGET_TEXT_LIMITS.items():
            with self.subTest(key=key):
                self.root = self.root/key
                self.root.mkdir()
                with self.assertRaisesRegex(ValueError, key + ' must be a string'):
                    self.declare(dict(self.declaration, **{key: 'x'*(limit+1)}))
                self.assertFalse((self.root/'target.json').exists())

    def test_gate_and_floor_are_closed(self):
        for declaration, message in ((dict(self.declaration, gate='anything'), 'gate'),
                                     (dict(self.declaration, min_holds_per_host=0), 'min_holds_per_host'),
                                     (dict(self.declaration, min_holds_per_host=True), 'min_holds_per_host')):
            with self.subTest(message=message), self.assertRaisesRegex(ValueError, message):
                self.declare(declaration)
        self.assertFalse((self.root/'target.json').exists())

    @unittest.skipUnless(shutil.which('go'), 'Go toolchain is required for the roll-up generator')
    def test_rollup_passes_only_when_the_owner_flag_is_true(self):
        repository = Path(__file__).resolve().parents[1]
        module = repository/'server'
        source = module/'internal/store/qual/cmd/rollup/testdata/allpass'
        if not source.is_dir():
            self.skipTest('roll-up fixtures are not available')
        artifacts = self.root/'artifacts'
        artifacts.mkdir()
        cells = []
        for directory in sorted(source.iterdir()):
            if not (directory/'receipt.json').is_file():
                continue
            cell = artifacts/directory.name
            cell.mkdir()
            shutil.copyfile(directory/'receipt.json', cell/'receipt.json')
            shutil.copyfile(directory/'raw.ndjson', cell/'raw.ndjson')
            cells.append(cell)
        self.assertEqual(len(cells), 6)
        binary = self.root/'rollup'
        subprocess.run(['go', 'build', '-o', str(binary), './internal/store/qual/cmd/rollup'],
                       cwd=module, check=True, capture_output=True, timeout=300)
        fixture_declaration = dict(version='portico-class-target-v1', target='synthetic fixture target',
                                   target_flag=True, reason='synthetic fixture; not evidence',
                                   gate='cell_declared', min_holds_per_host=3, synthetic=True)

        def rollup():
            out = self.root/('out-' + str(len(list(self.root.glob('out-*')))))
            result = subprocess.run([str(binary), '-artifacts', str(artifacts), '-out', str(out)],
                                    cwd=module, capture_output=True, text=True, timeout=300)
            report = json.loads((out/'rollup.json').read_text())
            self.assertEqual(result.returncode == 0, report['verdict'] == 'pass')
            return report

        def prepare(declaration):
            for cell in cells:
                target = cell/'target.json'
                if target.exists():
                    target.unlink()
                receipt = json.loads((cell/'receipt.json').read_text())
                final = {'kind': 'final', 'complete': True}
                if declaration is not None:
                    final['target'] = dict(declaration, **{
                        'class': receipt['declared']['name'], 'profile': receipt['declared']['profile'],
                        'replicate': receipt['declared']['replicate']})
                receipt['wire'] = {'owner_final': final}
                c.q.atomic_json(cell/'receipt.json', receipt)
                c.write_owner_target(cell, receipt)

        prepare(None)
        self.assertFalse(any((cell/'target.json').exists() for cell in cells))
        self.assertNotEqual(rollup()['verdict'], 'pass', 'a missing target declaration must not pass')
        prepare(fixture_declaration)
        self.assertTrue(all((cell/'target.json').is_file() for cell in cells))
        self.assertEqual(rollup()['verdict'], 'pass')
        prepare(dict(fixture_declaration, target_flag=False))
        self.assertNotEqual(rollup()['verdict'], 'pass', 'a false owner flag must not pass')


class PopulationContent(unittest.TestCase):
    """Compatibility is keyed on population content, never on source bytes."""
    SCHEMA = 'a' * 64

    def manifest(self):
        reviewed = population.REVIEWED[population.CANONICAL_MOVIES]
        return {'version': 'portico-qualification-population-v1', 'source_commit': 'b' * 40,
                'schema_sha256': self.SCHEMA, 'engine': '3.53.4',
                'engine_source': 'synthetic engine identity', 'go_version': 'go1.26.10',
                'database_sha256': 'c' * 64, 'database_bytes': 4096,
                'facts': {'version': population.CANONICAL_MOVIES, 'seed': reviewed['seed'],
                          'residents': reviewed['residents'], 'library_id': 7,
                          'library_key': 'qualification-movies', 'first_id': 1000000001,
                          'last_id': 1000000000 + reviewed['residents'],
                          'counts': dict(reviewed['counts']),
                          'logical_sha256': reviewed['logical_sha256']}}

    def check(self, manifest):
        return population.require_content(manifest, schema=self.SCHEMA)

    def test_same_content_with_different_test_source_bytes_is_compatible(self):
        first, second = self.manifest(), self.manifest()
        # Different producer/test source bytes, image bytes and engine identity
        # around identical population content must not change compatibility.
        second.update(source_commit='d' * 40, database_sha256='e' * 64,
                      engine_source='a different synthetic engine identity')
        self.assertEqual(self.check(first), self.check(second))
        self.assertEqual(self.check(first)['logical_sha256'],
                         population.REVIEWED[population.CANONICAL_MOVIES]['logical_sha256'])

    def test_changed_shape_is_refused(self):
        for shape in ('canonical-movies-v2', population.CANONICAL_MOVIES_SEARCH, '', None, []):
            manifest = self.manifest()
            manifest['facts']['version'] = shape
            with self.subTest(shape=shape), self.assertRaisesRegex(ValueError, 'shape'):
                self.check(manifest)

    def test_changed_residents_are_refused(self):
        for residents in (999999, 1000001, 0, True, '1000000'):
            manifest = self.manifest()
            manifest['facts']['residents'] = residents
            with self.subTest(residents=residents), self.assertRaisesRegex(ValueError, 'residents'):
                self.check(manifest)

    def test_changed_logical_digest_is_refused(self):
        for digest in ('0' * 64, 'eb1b1a877268c5f57744c153161bc2700dedc2ed2d5e0acc11018d31cc89ac1e',
                       'eb1b1a877268c5f57744c153161bc2700dedc2ed2d5e0acc11018d31cc89ac1', None, 1):
            manifest = self.manifest()
            manifest['facts']['logical_sha256'] = digest
            with self.subTest(digest=digest), self.assertRaisesRegex(ValueError, 'logical digest'):
                self.check(manifest)

    def test_schema_mismatch_is_refused_and_never_relabeled(self):
        for schema in ('f' * 64, '9d9aa39dfcdb99d4943f61b91ce7307f17026bf5ff7afed36a248f6b55b21f05', None):
            manifest = self.manifest()
            manifest['schema_sha256'] = schema
            with self.subTest(schema=schema), self.assertRaisesRegex(ValueError, 'schema'):
                self.check(manifest)

    def test_changed_seed_and_counts_are_refused(self):
        manifest = self.manifest()
        manifest['facts']['seed'] = 1
        with self.assertRaisesRegex(ValueError, 'seed'):
            self.check(manifest)
        manifest = self.manifest()
        manifest['facts']['counts']['catalog_kinds'] = 2
        with self.assertRaisesRegex(ValueError, 'counts'):
            self.check(manifest)
        manifest = self.manifest()
        manifest['facts']['counts'].pop('catalog_entity_terms')
        with self.assertRaisesRegex(ValueError, 'counts'):
            self.check(manifest)
        manifest = self.manifest()
        manifest['facts']['counts']['extra_table'] = 0
        with self.assertRaisesRegex(ValueError, 'counts'):
            self.check(manifest)

    def test_malformed_content_record_is_refused(self):
        for manifest in ({}, {'schema_sha256': self.SCHEMA}, {'schema_sha256': self.SCHEMA, 'facts': []},
                         {'schema_sha256': self.SCHEMA, 'facts': {'version': population.CANONICAL_MOVIES}}):
            with self.subTest(manifest=manifest), self.assertRaisesRegex(ValueError, 'population'):
                self.check(manifest)


class CompatibilitySources(unittest.TestCase):
    """Non-test source that interprets an image must not move; the seed test may."""
    SEED_TEST = 'server/internal/store/writer/qualification_population_seed_test.go'

    def test_non_test_source_changes_are_refused_by_name(self):
        for changed in (['server/go.mod'], ['server/go.sum'],
                        ['server/internal/store/schema/schema.sql'],
                        ['server/internal/store/writer/scan_metadata.go'],
                        ['server/internal/sorttext/sort.go'],
                        ['server/go.mod', 'ci/qual_class.py']):
            with self.subTest(changed=changed), self.assertRaisesRegex(ValueError, 'non-test source differs'):
                c.require_unchanged_sources(changed)
        for root in c.COMPATIBILITY_PATHS:
            with self.subTest(root=root), self.assertRaisesRegex(ValueError, root):
                c.require_unchanged_sources([root])

    def test_only_the_seed_test_source_change_is_not_a_path_change(self):
        self.assertNotIn(self.SEED_TEST, c.COMPATIBILITY_PATHS)
        c.require_unchanged_sources([])
        c.require_unchanged_sources([self.SEED_TEST])
        c.require_unchanged_sources([self.SEED_TEST, 'ci/qual_class.py', 'docs/readme.md'])
        self.assertEqual(c.compatibility_changes([self.SEED_TEST]), [])


if __name__ == '__main__': unittest.main()
