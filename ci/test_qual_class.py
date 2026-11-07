"""Synthetic transport controls only; no database or qualification samples."""
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import qual_class as c


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

    def test_owner_completion_is_not_qualification(self):
        path = self.records([{'kind': 'start'}, {'kind': 'final', 'complete': True}])
        result = c.verify_stream(path, self.cfg)
        self.assertIsNone(result['qualification_verdict'])
        self.assertEqual(result['records'], 2)
        self.assertNotIn('p99', result)

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

    def test_finalize_retains_failure_and_file_hashes(self):
        output = self.root/'partial'
        output.mkdir()
        (output/'raw.ndjson').write_text('partial raw prefix\n')
        c.finalize(output)
        self.assertEqual(json.loads((output/'receipt.json').read_text())['status'], 'failed')
        files = json.loads((output/'files.json').read_text())
        self.assertEqual({f['path'] for f in files}, {'raw.ndjson', 'receipt.json'})


if __name__ == '__main__': unittest.main()
