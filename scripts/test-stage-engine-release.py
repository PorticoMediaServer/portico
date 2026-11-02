#!/usr/bin/env python3
"""Declared API/ZIP fixtures: exact intake and refusal, never a real engine."""
import copy
from datetime import datetime, timezone
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import warnings
import zipfile

spec = importlib.util.spec_from_file_location('stage_engine', Path(__file__).with_name('stage-engine-release.py'))
M = importlib.util.module_from_spec(spec)
spec.loader.exec_module(M)
SOURCE = 'a' * 40
NOW = datetime(2026, 11, 1, 12, tzinfo=timezone.utc)


def zipped(entries):
    output = io.BytesIO()
    with warnings.catch_warnings(), zipfile.ZipFile(output, 'w') as archive:
        warnings.simplefilter('ignore', UserWarning)
        for name, content in entries:
            archive.writestr(name, content)
    return output.getvalue()


def fixtures():
    locator = dict(schemaVersion=1, repository=M.REPOSITORY, sourceSHA=SOURCE, runId=12, runAttempt=2, targets={})
    run = dict(id=12, run_attempt=2, head_sha=SOURCE, head_branch='main', event='workflow_dispatch',
               path=M.WORKFLOW, status='completed', conclusion='success',
               repository={'full_name': M.REPOSITORY}, head_repository={'full_name': M.REPOSITORY},
               run_started_at='2026-11-01T10:00:00Z', updated_at='2026-11-01T11:00:00Z')
    responses = {'actions/runs/12/attempts/2': json.dumps(run).encode()}
    index = 100
    for target in M.REQUIRED:
        entries = locator['targets'][target] = {}
        for kind in ('product', 'native'):
            index += 1
            contents = ([('NOT-FOR-USE.txt', 'Controlled fixture, no engine'),
                         ('bundle.tar', 'Inert opaque fixture; not a valid engine'),
                         ('transfer.json', '{"controlledFixture":true}')]
                        if kind == 'product' else
                        [('cold-native.json', '{"controlledFixture":true}'),
                         ('gates/native-engine.json', '{"controlledFixture":true}')])
            archive = zipped(contents)
            sha = hashlib.sha256(archive).hexdigest()
            entries[kind] = dict(artifactId=index, sha256=sha)
            name = 'engine-cold-' + ('native-' if kind == 'native' else '') + target + '-' + SOURCE + '-NOT-FOR-USE'
            metadata = dict(id=index, name=name, expired=False, digest='sha256:' + sha,
                            size_in_bytes=len(archive), workflow_run=dict(id=12, head_sha=SOURCE, head_branch='main'),
                            created_at='2026-11-01T10:30:00Z', expires_at='2026-11-15T10:30:00Z')
            endpoint = 'actions/artifacts/' + str(index)
            responses[endpoint] = json.dumps(metadata).encode()
            responses[endpoint + '/zip'] = archive
    return locator, responses


class ActionsIntake(unittest.TestCase):
    def stage(self, locator, responses, output):
        calls = []
        def transport(endpoint, destination, limit):
            calls.append(endpoint)
            self.assertLessEqual(len(responses[endpoint]), limit)
            with destination.open('xb') as stream:
                stream.write(responses[endpoint])
        M.stage(locator, output, transport=transport, now=NOW)
        return calls

    def test_exact_bytes_and_api_receipts_survive_without_any_acceptance_grant(self):
        locator, responses = fixtures()
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / 'staged'
            calls = self.stage(locator, responses, output)
            self.assertEqual(len(calls), 17)
            receipt = json.loads((output / 'intake.json').read_text())
            self.assertEqual(receipt['context'], 'read-only-intake')
            for field in ('qualified', 'release', 'readyForPublication'):
                self.assertIs(receipt[field], False)
            for target, entries in locator['targets'].items():
                for kind, pin in entries.items():
                    raw = output / 'actions' / (target + '-' + kind + '.zip')
                    self.assertEqual(M.digest(raw), pin['sha256'])
                    self.assertEqual(raw.read_bytes(), responses['actions/artifacts/' + str(pin['artifactId']) + '/zip'])
            self.assertIn('windows-x64', {p.name for p in (output / 'inputs').iterdir()})
            self.assertFalse((output / 'inputs/actions').exists())

    def test_wrong_branch_workflow_source_attempt_event_or_status_refuses_before_download(self):
        for key, value in [('head_branch', 'topic'), ('path', '.github/workflows/release.yml'),
                           ('head_sha', 'b' * 40), ('run_attempt', 1), ('id', True),
                           ('event', 'pull_request'), ('status', 'in_progress'), ('conclusion', 'failure'),
                           ('head_repository', {'full_name': 'fork/portico'})]:
            locator, responses = fixtures()
            run = json.loads(responses['actions/runs/12/attempts/2']); run[key] = value
            responses['actions/runs/12/attempts/2'] = json.dumps(run).encode()
            with self.subTest(key=key), tempfile.TemporaryDirectory() as temporary:
                output = Path(temporary) / 'staged'
                with self.assertRaisesRegex(ValueError, 'main toolchain'):
                    self.stage(locator, responses, output)
                self.assertEqual(list(Path(temporary).iterdir()), [])

    def test_ambiguous_locator_ids_unknown_targets_missing_pair_and_bool_ids_refuse(self):
        locator, _ = fixtures()
        mutations = [lambda x: x['targets'].pop('linux-arm64'),
                     lambda x: x['targets'].update({'windows-arm64': {}}),
                     lambda x: x['targets']['linux-x64'].pop('native'),
                     lambda x: x.update(runAttempt=True),
                     lambda x: x['targets']['linux-x64']['native'].update(artifactId=101),
                     lambda x: x['targets']['linux-x64']['native'].update(sha256='latest')]
        for mutate in mutations:
            changed = copy.deepcopy(locator); mutate(changed)
            with self.assertRaises(ValueError):
                M.validate_locator(changed)

    def test_expiry_digest_identity_and_attempt_creation_window_refuse_atomically(self):
        changes = [('expired', True), ('digest', 'sha256:' + 'f' * 64), ('id', 999),
                   ('name', 'latest'), ('size_in_bytes', True),
                   ('created_at', '2026-11-01T09:59:59Z'),
                   ('created_at', '2026-11-01T11:00:01Z'),
                   ('expires_at', '2026-11-01T11:59:59Z'),
                   ('expires_at', '2026-11-16T10:30:00Z'),
                   ('workflow_run', dict(id=11, head_sha=SOURCE, head_branch='main'))]
        for key, value in changes:
            locator, responses = fixtures()
            endpoint = 'actions/artifacts/101'
            metadata = json.loads(responses[endpoint]); metadata[key] = value
            responses[endpoint] = json.dumps(metadata).encode()
            with self.subTest(key=key, value=value), tempfile.TemporaryDirectory() as temporary:
                with self.assertRaises(ValueError):
                    self.stage(locator, responses, Path(temporary) / 'staged')
                self.assertEqual(list(Path(temporary).iterdir()), [])

    def test_tampered_download_duplicate_json_and_existing_or_linked_output_refuse(self):
        for change in ('bytes', 'json'):
            locator, responses = fixtures()
            if change == 'bytes':
                responses['actions/artifacts/101/zip'] += b'tamper'
            else:
                responses['actions/artifacts/101'] = b'{"id":101,"id":102}'
            with self.subTest(change=change), tempfile.TemporaryDirectory() as temporary:
                with self.assertRaises(ValueError):
                    self.stage(locator, responses, Path(temporary) / 'staged')
                self.assertEqual(list(Path(temporary).iterdir()), [])
        locator, responses = fixtures()
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary); existing = root / 'existing'; existing.mkdir()
            sentinel = existing / 'sentinel'; sentinel.write_text('keep')
            link = root / 'linked'; link.symlink_to(existing, target_is_directory=True)
            for output in (existing, link / 'new'):
                with self.assertRaises(ValueError):
                    self.stage(locator, responses, output)
            self.assertEqual(sentinel.read_text(), 'keep')

    def test_actual_zip_traversal_links_case_collisions_inventory_and_expansion_refuse(self):
        linked = zipfile.ZipInfo('cold-native.json'); linked.external_attr = 0o120777 << 16
        cases = [[('../escape', 'x')], [('gates\\escape.json', 'x')],
                 [(linked, 'elsewhere')], [('cold-native.json', 'x'), ('COLD-NATIVE.JSON', 'y')],
                 [('gates', 'file'), ('gates/native-engine.json', '{}'), ('cold-native.json', '{}')],
                 [('cold-native.json', '{}'), ('gates/native-engine.json', '{}'), ('unexpected.exe', 'x')]]
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            for index, entries in enumerate(cases):
                path = root / (str(index) + '.zip'); path.write_bytes(zipped(entries))
                with self.subTest(index=index), self.assertRaises(ValueError):
                    M.extract(path, root / ('out-' + str(index)), 'native')
            path = root / 'large.zip'
            path.write_bytes(zipped([('cold-native.json', 'x' * 200), ('gates/native-engine.json', '{}')]))
            with patch.dict(M.LIMITS, native=100), self.assertRaisesRegex(ValueError, 'expansion'):
                M.extract(path, root / 'expanded', 'native')
            self.assertFalse((root / 'expanded').exists())


if __name__ == '__main__':
    unittest.main()
